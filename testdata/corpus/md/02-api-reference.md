# API Reference

## Pool.Get

`Get` returns a connection from the pool, blocking until one is free.

```go
func (p *Pool) Get(ctx context.Context) (*Conn, error) {
	select {
	case c := <-p.idle:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
```

## Pool.Put

Return a connection with `Put`. A broken connection is closed instead.

```js
// # this comment is not a heading
pool.put(conn);
```

## Shell helpers

```bash
# Start the pool exporter
# ## also not a heading
./exporter --listen :9090
```

### Nested fences

````markdown
```sql
SELECT count(*) FROM connections;
```
````

Text after the four-backtick fence.

```
unterminated fence at end of file
# Not a heading inside an open fence
