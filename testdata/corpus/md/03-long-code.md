# Rate Limiter Source

The full token bucket implementation is kept in one fenced block so it is never split.

```go
package ratelimit

import (
	"errors"
	"sync"
	"time"
)

// ErrLimited is returned when the bucket has no tokens left.
var ErrLimited = errors.New("ratelimit: too many requests")

// Bucket is a token bucket rate limiter.
type Bucket struct {
	mu       sync.Mutex
	capacity int
	tokens   int
	refill   time.Duration
	last     time.Time
	now      func() time.Time
}

// NewBucket returns a full bucket that adds one token per refill interval.
func NewBucket(capacity int, refill time.Duration) *Bucket {
	return &Bucket{
		capacity: capacity,
		tokens:   capacity,
		refill:   refill,
		last:     time.Now(),
		now:      time.Now,
	}
}

// Take removes one token or returns ErrLimited.
func (b *Bucket) Take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.add()
	if b.tokens == 0 {
		return ErrLimited
	}
	b.tokens--
	return nil
}

// TakeN removes n tokens at once or none of them.
func (b *Bucket) TakeN(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.add()
	if b.tokens < n {
		return ErrLimited
	}
	b.tokens -= n
	return nil
}

// Available reports the tokens left after refilling.
func (b *Bucket) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.add()
	return b.tokens
}

// add refills tokens for the time elapsed since the last call.
func (b *Bucket) add() {
	now := b.now()
	elapsed := now.Sub(b.last)
	if elapsed < b.refill {
		return
	}
	gained := int(elapsed / b.refill)
	b.tokens += gained
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = b.last.Add(time.Duration(gained) * b.refill)
}

// Limiter keeps one bucket per client key.
type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*Bucket
	capacity int
	refill   time.Duration
}

// NewLimiter returns a limiter that creates buckets lazily.
func NewLimiter(capacity int, refill time.Duration) *Limiter {
	return &Limiter{
		buckets:  make(map[string]*Bucket),
		capacity: capacity,
		refill:   refill,
	}
}

// Allow takes one token from the bucket for key.
func (l *Limiter) Allow(key string) bool {
	return l.bucket(key).Take() == nil
}

// bucket returns the bucket for key, creating it on first use.
func (l *Limiter) bucket(key string) *Bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = NewBucket(l.capacity, l.refill)
		l.buckets[key] = b
	}
	return b
}

// Sweep drops buckets that are full again, so idle clients free memory.
func (l *Limiter) Sweep() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	dropped := 0
	for key, b := range l.buckets {
		if b.Available() == l.capacity {
			delete(l.buckets, key)
			dropped++
		}
	}
	return dropped
}

// Len reports how many client buckets are live.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Middleware rejects requests over the limit with status 429.
func Middleware(l *Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r)
		if !l.Allow(key) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, ErrLimited.Error(), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey picks the forwarded address when a trusted proxy set one.
func clientKey(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Stats is a snapshot of limiter counters for the metrics endpoint.
type Stats struct {
	Clients  int `json:"clients"`
	Capacity int `json:"capacity"`
	RefillMS int `json:"refill_ms"`
}

// Snapshot reports the current limiter counters.
func (l *Limiter) Snapshot() Stats {
	return Stats{
		Clients:  l.Len(),
		Capacity: l.capacity,
		RefillMS: int(l.refill / time.Millisecond),
	}
}

// StartSweeper runs Sweep on every tick until stop is closed.
func (l *Limiter) StartSweeper(every time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(every)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-t.C:
				l.Sweep()
			case <-stop:
				return
			}
		}
	}()
}
```

## Usage

Call `Allow` with the client address before serving each request.
