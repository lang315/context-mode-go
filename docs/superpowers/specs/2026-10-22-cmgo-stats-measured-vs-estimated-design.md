# cmgo: lifetime and statusline show measured kept only; the session redirect line stays estimated (design)

Date: 2026-10-22. Rev 2 (owner: simplified). Extends stats spec §2 and §3 (`2026-10-21-cmgo-stats-design.md`). Evidence: the 2026-10-22 baseline audit.

## 0. Problem

`ctx_stats` prints `All projects · 14.66 MB kept out`, and the statusline prints the same number as `lifetime`. It is the sum of two different things:

- **measured**: `stats_totals.bytes_kept`, bytes the sandbox processed and did not return (2.73 MB here, of which 2.67 MB is one test call; 68.3 KB on clean rows);
- **estimated**: `SUM(session_events.bytes_avoided)` over every `redirect` event (11.93 MB, 58 events), a guess made by the hook.

The estimate is mostly wrong in kind. The Read hook adds a tip for a file over 50,000 B and records `read-redirected` with `bytes_avoided = file size`, but the Read still runs. In the cmgo-era events matched to a transcript, the Read ran afterwards in 10 of 14 (71%), carrying 1.87 of the 2.61 MB claimed. Measured bytes were 0.5% of the headline.

Goal: the lifetime figure and the statusline show only what was measured, and a Read tip is not counted as a redirect.

Success: (1) the lifetime kept figure on `ctx_stats` and the statusline equals `SUM(stats_totals.bytes_kept)`; (2) the statusline lifetime query no longer reads `session_events`; (3) the session line `Redirected by hooks` stays, is labelled estimated, and counts no `read-redirected` event in either number; (4) no schema change and no rewrite of stored data.

## 1. Definitions

- **Measured kept** = `SUM(stats_totals.bytes_kept)`. Unchanged meaning; it is now the whole lifetime figure.
- **Redirect (session line)** = a `session_events` row of this session with `category = 'redirect'` and `type <> 'read-redirected'`: `bash-redirected` (fixed 8,192 B) and `webfetch-redirected` (fixed 16,384 B). Its `bytes_avoided` is an estimate.
- A `read-redirected` event stays in `session_events` (session memory keeps it, nothing is deleted); it is a tip, not a redirect, so it is excluded from the session line's count and bytes.
- There is no lifetime estimate figure anywhere: the estimates are shown for the current session only.

## 2. Display

`ctx_stats` (stats spec §2):

```
  Redirected by hooks    <n> calls  ·  ~<kb> (estimated)       this session; Read tips excluded from both numbers
...
All projects  ·  <kb measured> kept out  ·  [<n> ctx_execute call(s) not measured  ·  ]<n> sessions  ·  since <date>
```

Only the numbers change: `kept out` now means measured bytes only, and `(estimated)` is added to the session redirect line.

Statusline (stats spec §3): the lines are unchanged; `lifetime` and the no-session-bytes line show the measured figure. No estimate appears.

## 3. Changes

- `internal/store/stats.go`: `lifetimeKept` becomes `(SELECT COALESCE(SUM(bytes_kept), 0) FROM stats_totals)`. The session `Redirects` and `RedirectBytes` query gets `AND type <> 'read-redirected'`. `StatsSnapshot` keeps its fields.
- `internal/stats/report.go`: `(estimated)` on the `Redirected by hooks` line.
- `internal/store/readonly.go`, `internal/hookcmd`, `internal/mcpserver/ctxstats.go`: no code change; `SessionLine` and `Stats` use the new `lifetimeKept`.
- No migration. `routing` and the capture write path are not touched: the Read hook still writes its TS-parity event.
- Docs: stats spec §2 and §3 text, `testdata/golden/KNOWN-DIFFS.md` entry `stats:lifetime-split`.

## 4. Statusline cost

The statusline runs on every refresh with a 10 ms budget. After this change its lifetime figure is one sum over `stats_totals` (one row per project), so it no longer touches `session_events` and no longer needs the partial redirect index. `TestLifetimeKeptUsesRedirectIndex` is obsolete and is replaced by `TestLifetimeKeptReadsOnlyStatsTotals`: the query plan of `lifetimeKept` mentions `stats_totals` and not `session_events`. The partial index `session_events_redirect` stays (the session redirect query still filters on `category`); no migration.

## 5. Compatibility

- Existing rows are read differently, not changed. On the 2026-10-22 DB copy the lifetime figure drops from 14.66 MB to 2.73 MB (2.67 MB of it one test call). The 58 redirect events, 12.5 MB of estimates (53 Read tips, 5 `bash-redirected`), no longer feed any lifetime or statusline number. The PR body shows the before and after on a copy.
- This change adds no column and no read of a new one. A schema 4 database works unchanged. The statusline on a schema 3 database already falls back to `cmgo  ●  ready` (its per-session query reads `unmeasured_calls`, added by migration 4) until a writer hook migrates the file; that is not changed here.

## 6. KNOWN-DIFFS

`stats:lifetime-split`: the `All projects` line and the statusline lifetime show measured sandbox bytes only (`stats_totals`), without the hook-estimated `bytes_avoided`; the session line `Redirected by hooks` excludes Read tips and is labelled `(estimated)`. No golden case reads these lines, so none is skipped. `TestKnownDiffsListsLifetimeSplit` pins the entry.

## 7. Testing (TDD, red first)

- `store`: with redirect events present (Read tips, Bash and WebFetch redirects), `Stats` lifetime equals the `stats_totals` sum; session `Redirects`/`RedirectBytes` count the Bash and WebFetch events and ignore Read tips; a session with only Read tips has 0 and 0. Existing expectations that added redirect bytes to the lifetime (`stats_test.go` lines around 155, 385, `purge_test.go` 127) are updated to the measured sum.
- `store`: `TestLifetimeKeptReadsOnlyStatsTotals` (§4) replaces `TestLifetimeKeptUsesRedirectIndex`.
- `stats.Format`: the `(estimated)` text.
- `mcpserver`: `ctx_stats` end to end after one shell run and one Bash redirect event: the lifetime line has no redirect bytes, the session line has them.
- `cmgo-hook` deps unchanged; `gofmt`, `go vet`, `go test`, `-race`. The statusline hook benchmark (`bench/hook-latency.py`) is run once as a regression check.

## 8. Out of scope

- Showing a lifetime estimate in any form; changing the 8,192 B and 16,384 B constants; crediting an estimate only when a `ctx_*` call follows.
- Stopping the Read hook from writing `bytes_avoided`.
- Any change to the TS-parity events or session memory.
