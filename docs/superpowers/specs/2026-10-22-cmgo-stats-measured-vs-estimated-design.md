# cmgo: lifetime and statusline show measured kept apart from hook-estimated avoided (design)

Date: 2026-10-22. Rev 1. Extends stats spec §2 and §3 (`2026-10-21-cmgo-stats-design.md`). Evidence: the 2026-10-22 baseline audit.

## 0. Problem

`ctx_stats` prints `All projects · 14.66 MB kept out`, and the statusline prints the same number as `lifetime`. It is the sum of two different things:

- **measured**: `stats_totals.bytes_kept`, bytes the sandbox processed and did not return (2.73 MB here, of which 2.67 MB is one test call; 68.3 KB on clean rows);
- **estimated**: `SUM(session_events.bytes_avoided)` over every `redirect` event (11.93 MB, 58 events), a guess made by the hook.

The estimate is mostly wrong in kind. The Read hook adds a tip for a file over 50,000 B and records `read-redirected` with `bytes_avoided = file size`, but the Read still runs. In the cmgo-era events matched to a transcript, the Read ran afterwards in 10 of 14 (71%), carrying 1.87 of the 2.61 MB claimed. Measured bytes were 0.5% of the headline.

Goal: the two figures are shown apart, and a Read tip is not counted as avoided bytes.

Success: (1) the lifetime kept figure equals measured bytes only; (2) the estimate is shown separately and labelled estimated, only when above 0; (3) no `read-redirected` event adds to any avoided or redirect figure; (4) no schema change and no rewrite of stored data.

## 1. Definitions

- **Measured kept** = `SUM(stats_totals.bytes_kept)`. Unchanged meaning, no longer mixed with anything.
- **Estimated avoided** = `SUM(bytes_avoided)` over `session_events` with `category = 'redirect'` and `type <> 'read-redirected'`. That is `bash-redirected` (fixed 8,192 B per event) and `webfetch-redirected` (fixed 16,384 B), whether or not the model then called a `ctx_*` tool. TS-imported events follow the same rule.
- A `read-redirected` event stays in `session_events` (session memory keeps it, nothing is deleted); it is a tip, not a redirect, so it is excluded from every count and byte figure below.

## 2. Display

`ctx_stats` (stats spec §2):

```
  Redirected by hooks    <n> calls  ·  ~<kb> (estimated)       n and bytes: redirects of this session, Read tips excluded
...
All projects  ·  <kb measured> kept out  ·  ~<kb> avoided by hooks (estimated)  ·  <n> ctx_execute call(s) not measured  ·  <n> sessions  ·  since <date>
```

- `kept out` keeps its label and now means measured bytes only.
- `~<kb> avoided by hooks (estimated)` appears only when the estimate is above 0, right after the kept figure.
- The `Redirected by hooks` line is kept as it is (it was already apart from `Kept`), with `(estimated)` added and the Read tips left out of both numbers.

Statusline (stats spec §3):

| Case | Line |
|---|---|
| session has no kept bytes yet | `cmgo  ●  <kb measured> kept out lifetime` + ` · ~<kb> est. avoided` when above 0 |
| otherwise | `cmgo  ●  <kb session> this chat  ·  <kb measured> lifetime  ·  <pct>% kept out` + ` · ~<kb> est. avoided` when above 0 |

`pct` is unchanged (session measured kept over session raw). The fallback `cmgo  ●  ready` is unchanged.

## 3. Changes

- `internal/store/stats.go`: `lifetimeKept` becomes `(SELECT COALESCE(SUM(bytes_kept), 0) FROM stats_totals)`. New `lifetimeAvoided` = the redirect sum with the `type <> 'read-redirected'` filter. `StatsSnapshot.LifetimeAvoided`; the session `Redirects` and `RedirectBytes` queries get the same filter.
- `internal/store/readonly.go`: `SessionLine` also returns the avoided estimate (the statusline reads both in one call).
- `internal/stats`: `Report.LifetimeAvoided`, `Line.LifetimeAvoided`, the format lines above.
- `internal/hookcmd/statusline.go`, `internal/mcpserver/ctxstats.go`: pass the new field through.
- No migration. `routing` and the capture write path are not touched: the Read hook still writes its TS-parity event.
- Docs: stats spec §2 and §3 text, `README.md` row for `ctx_stats` if it implies one figure, `testdata/golden/KNOWN-DIFFS.md` entry `stats:lifetime-split`.

## 4. Statusline cost

The statusline runs on every refresh with a 10 ms budget. Today `lifetimeKept` is covered by the partial index `session_events_redirect(bytes_avoided) WHERE category = 'redirect'`. The new filter reads `type`, so SQLite looks up each redirect row (58 now; it grows with Bash and WebFetch redirects, not with Read tips in the future). `TestLifetimeKeptUsesRedirectIndex` is updated to assert the plan uses `session_events_redirect` and never scans `session_events`. If the statusline benchmark shows more than a 2 ms increase at 10,000 redirect rows, add a partial index `ON session_events(bytes_avoided) WHERE category = 'redirect' AND type <> 'read-redirected'` as migration 5 in the same PR.

## 5. Compatibility

- Existing rows are read differently, not changed. On the 2026-10-22 DB copy the lifetime figure drops from 14.66 MB to 2.73 MB measured (2.67 MB of it one test call) plus 40 KB estimated (5 `bash-redirected` events; the 53 Read tips, 12.47 MB, are no longer counted), which is the point; the PR body shows the before and after on a copy of the real DB.
- `stats_totals` already holds measured bytes only; nothing to backfill.
- A database at schema 3 or 4 works unchanged (no new column).

## 6. KNOWN-DIFFS

`stats:lifetime-split`: the TS plugin has no cmgo lifetime or statusline figure to match; the entry records that the `All projects` line and the statusline no longer include hook-estimated bytes, that `Redirected by hooks` excludes Read tips, and the `~... avoided by hooks (estimated)` suffix. No golden case reads these lines, so none is skipped. `TestKnownDiffsListsLifetimeSplit` pins the entry.

## 7. Testing (TDD, red first)

- `store`: lifetime equals the `stats_totals` sum even with redirect events; `LifetimeAvoided` counts `bash-redirected` and `webfetch-redirected` and ignores `read-redirected`; session `Redirects`/`RedirectBytes` ignore Read tips; an old fixture with only Read tips has `LifetimeAvoided` 0.
- `store` plan test for the index (see §4); a statusline benchmark with 10,000 redirect rows.
- `stats.Format` and `Line`: suffix present above 0, absent at 0, both lifetime variants.
- `mcpserver`: `ctx_stats` end to end after one shell run and one Bash redirect event.
- `cmgo-hook` deps unchanged (formatting and store only); `gofmt`, `go vet`, `go test`, `-race`.

## 8. Out of scope

- Changing the 8,192 B and 16,384 B constants or crediting an estimate only when a `ctx_*` call follows.
- Stopping the Read hook from writing `bytes_avoided`.
- Any change to the TS-parity events or session memory.
