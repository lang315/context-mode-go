package mcpserver

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"
)

const (
	fetchTTLDefaultMs   = 24 * 60 * 60 * 1000 // FETCH_TTL_MS, S:3118
	fetchPreviewLimit   = 3072                // FETCH_PREVIEW_LIMIT, S:3119
	fetchBatchPreview   = 384                 // FETCH_BATCH_PREVIEW_LIMIT, S:3622
	fetchTruncateSuffix = "\n\n…[truncated — use ctx_search() for full content]"
)

// fetchNumCPU is os.cpus().length; a var so tests can pin the CPU cap.
var fetchNumCPU = runtime.NumCPU

// fetchOutcome is one URL's final state (TS Finalized, S:3536-3540).
type fetchOutcome struct {
	kind    string // "cached" | "fetched" | "error"
	url     string
	label   string
	chunks  int
	age     string // cached
	ttl     string // cached
	bytes   int    // fetched
	preview string // fetched
	err     string // error
	reason  string // error: "exit" | "empty" | "throw"
}

// fetchCacheKey is composeFetchCacheKey, fetch-cache.ts:13-15.
func fetchCacheKey(source string, hasSource bool, url string) string {
	if !hasSource {
		return url
	}
	return source + "::" + url
}

// fetchAge formats a cache age in ms, S:3307-3310.
func fetchAge(ageMs int64) string {
	if h := ageMs / (60 * 60 * 1000); h > 0 {
		return fmt.Sprintf("%dh ago", h)
	}
	if m := ageMs / (60 * 1000); m > 0 {
		return fmt.Sprintf("%dm ago", m)
	}
	return "just now"
}

// fetchTTL is formatFetchTtl, S:3121-3131.
func fetchTTL(ttlMs int64) string {
	const minute, hour, day = 60 * 1000, 60 * 60 * 1000, 24 * 60 * 60 * 1000
	switch {
	case ttlMs == 0:
		return "0ms"
	case ttlMs%day == 0:
		return fmt.Sprintf("%dd", ttlMs/day)
	case ttlMs%hour == 0:
		return fmt.Sprintf("%dh", ttlMs/hour)
	case ttlMs%minute == 0:
		return fmt.Sprintf("%dm", ttlMs/minute)
	}
	return fmt.Sprintf("%dms", ttlMs)
}

// fetchPreview is the preview cut in indexFetched, S:3400-3402.
// shortcut: counts runes where TS counts UTF-16 units; differs only for astral characters.
func fetchPreview(body string) string {
	r := []rune(body)
	if len(r) <= fetchPreviewLimit {
		return body
	}
	return string(r[:fetchPreviewLimit]) + fetchTruncateSuffix
}

// fetchPoolSize is runPool's sizing, runPool.ts:42-55: capped is also true
// when the job count, not the CPU count, lowered the value.
func fetchPoolSize(requested, jobs int, capByCPU bool) (eff int, capped bool) {
	requested = max(1, requested)
	cpuCap := requested
	if capByCPU {
		cpuCap = max(1, fetchNumCPU())
	}
	eff = min(requested, cpuCap, jobs)
	return eff, eff < requested
}

// fetchSingleText is the legacy single-URL response, S:3573-3617.
func fetchSingleText(o fetchOutcome) (string, bool) {
	switch o.kind {
	case "cached":
		return fmt.Sprintf("Cached: **%s** — %d sections, indexed %s (fresh, TTL: %s).\nTo refresh: call ctx_fetch_and_index again with `force: true`.\n\nYou MUST call ctx_search() to answer questions about this content — this cached response contains no content.\nUse: ctx_search(queries: [...], source: \"%s\")",
			o.label, o.chunks, o.age, o.ttl, o.label), false
	case "fetched":
		return strings.Join([]string{
			fmt.Sprintf("Fetched and indexed **%d sections** (%s) from: %s", o.chunks, KB(o.bytes), o.label),
			fmt.Sprintf("Full content indexed in sandbox — use ctx_search(queries: [...], source: \"%s\") for specific lookups.", o.label),
			"",
			"---",
			"",
			o.preview,
		}, "\n"), false
	}
	switch o.reason {
	case "empty":
		return fmt.Sprintf("Fetched %s but got empty content", o.url), true
	case "exit":
		return fmt.Sprintf("Failed to fetch %s: %s", o.url, o.err), true
	}
	return "Fetch error: " + o.err, true
}

// fetchBatchText is the aggregated batch response, S:3619-3671; isError
// only when every URL failed.
func fetchBatchText(outs []fetchOutcome, eff int, capped bool) (string, bool) {
	var lines, snippets []string
	var sections, bytes, cached, fetched, errs int
	for _, o := range outs {
		switch o.kind {
		case "cached":
			cached++
			lines = append(lines, fmt.Sprintf("- [cache] %s — %d sections (%s, TTL: %s)", o.label, o.chunks, o.age, o.ttl))
		case "fetched":
			fetched++
			sections += o.chunks
			bytes += o.bytes
			lines = append(lines, fmt.Sprintf("- [new]   %s — %d sections (%s)", o.label, o.chunks, KB(o.bytes)))
			snippet := o.preview
			if r := []rune(snippet); len(r) > fetchBatchPreview {
				snippet = strings.TrimRightFunc(string(r[:fetchBatchPreview]), unicode.IsSpace) + "…"
			}
			snippets = append(snippets, fmt.Sprintf("### %s\n\n%s", o.label, snippet))
		default:
			errs++
			lines = append(lines, fmt.Sprintf("- [err]   %s: %s", o.url, o.err))
		}
	}
	capNote := ""
	if capped {
		capNote = fmt.Sprintf(" cap=%d/%dcpu", eff, fetchNumCPU())
	}
	noun := "sections"
	if sections == 1 {
		noun = "section"
	}
	header := fmt.Sprintf("fetched %d c=%d%s. ok=%d cache=%d err=%d. %d %s %s.",
		len(outs), eff, capNote, fetched, cached, errs, sections, noun, KB(bytes))
	text := append([]string{header, ""}, lines...)
	text = append(text, "", `ctx_search(queries: [...], source: "<label>") for full content.`)
	if len(snippets) > 0 {
		text = append(append(text, "", "---", ""), snippets...)
	}
	return strings.Join(text, "\n"), errs == len(outs)
}
