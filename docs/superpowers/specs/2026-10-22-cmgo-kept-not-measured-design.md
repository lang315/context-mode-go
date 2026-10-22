# cmgo: show "not measured" for `ctx_execute` runs whose kept bytes cannot be counted (design)

Date: 2026-10-22. Rev 2 (owner: one column). Extends stats spec §1.3 and §2 (`2026-10-21-cmgo-stats-design.md`).

## 0. Problem and goal

`ctx_execute` counts the bytes a run reads only for JS/TS (stderr counters) and Python (sandbox counters, PR #12). Shell and the compiled/other languages cannot be measured on macOS, so those calls record `bytes_kept = 0`. Shell is about 71% of `ctx_execute` calls. `ctx_stats` shows `0 B kept` for them, which reads as "no savings" when the truth is "unknown".

Goal: `ctx_stats` says which `ctx_execute` calls were not measured. Success:

1. A shell `ctx_execute` is stored as not measured, and the session report says so on the `ctx_execute` row.
2. Rows written before this change print exactly as today and nothing ever says "measured".
3. Kept totals and percentages do not change.

Out of scope: measuring shell; changing the statusline (TS parity and its 10 ms budget); changing `ctx_execute_file`, `ctx_batch_execute`, `ctx_fetch_and_index` (always measured).

## 1. Decision: count columns on `tool_stats`, not a per-language split

Q1. How is "measured" recorded? One new column on `tool_stats`, migration 4 (`user_version` 3 → 4), appended to `migrations`:

```sql
ALTER TABLE tool_stats ADD COLUMN unmeasured_calls INTEGER NOT NULL DEFAULT 0;
```

Summed like `calls`. Only `ctx_execute` writes a non-zero value; the report reads it only for that tool.

Why not a per-language split: the primary key is `(server_id, tool, window)`; adding language multiplies rows and changes the key (a table rebuild). Languages also mislead: Python with `CMGO_PY_COUNTERS=0`, a Background run or a failed hook setup is unmeasured too. The executor knows the truth, so it reports it.

## 2. Recording

- `executor.Result` gets `Measured bool`: true when the run was instrumented and its counters were read (the `wrapJS` path, or the Python counters file path). False for every other run, including Python with counters off, Background, `execute_file` and compile failures.
- `ctx_execute` classifies each call: `Run` returned and `!Measured` → unmeasured; everything else (a measured run, or a call that never ran code: argument error, security denial, runtime missing) → counted as 0. Nothing was left to measure, so `kept 0` is true.
- `StatsRecorder` gets one more entry, `RecordExec(d, tool, raw, res, unmeasured bool)`, with `Record` unchanged for the other tools. `statJob` carries `unmeasured` (0 or 1). `AddToolStat` keeps its signature (19 test callers) and delegates to `AddToolStatExec(..., unmeasured int64)`, which adds it in the same upsert. `stats_totals` is not changed (it has no per-tool data).
- A timeout or `os._exit` Python run counts as measured (the executor had instrumented it; the lower bound is already documented in the Python spec §5). Only runs the executor never instrumented are unmeasured.

## 3. Display

Q2. `ctx_stats`:

- **Session** (per-tool rows, `window`/`server_id` filter as today): the `ctx_execute` row gets a suffix only when `unmeasured_calls > 0`:

```
  ctx_execute            20 calls    1.2 KB kept   3.0 KB returned  ·  14 not measured
```

  A JS-only session, and every session that has only pre-migration rows, prints exactly as before.
- **Project:** the report has no kept figure per project (stats spec §2), so there is nothing to annotate. Not changed.
- **Lifetime** (`All projects` line): `kept out` is a lower bound. When `SUM(unmeasured_calls)` over `tool_stats` (all projects) is N > 0, the line gets ` · N ctx_execute calls not measured` after the kept figure (before `sessions`). One more subselect in the existing `Stats` query.
- Headline `Kept out of context` and `pct`: unchanged.
- Statusline: unchanged.

Q3. Backward compatibility. Existing rows get `unmeasured_calls = 0` and nothing rewrites them, so they print exactly as today. Nothing in the report says "measured", so no old row is relabeled as measured. The lifetime count covers only calls recorded after migration 4; the re-audit (task 3) splits old and new rows by the migration date.

TS-imported rows: `tsimport` does not write `tool_stats` (checked: no reference in `internal/tsimport`), so no imported row exists.

## 4. KNOWN-DIFFS

Q4. TS has no such field. Add `stats:kept-not-measured` to `testdata/golden/KNOWN-DIFFS.md`: the `ctx_stats` per-tool row and the `All projects` line may carry a "not measured" suffix. No golden case runs a non-JS/TS/Python `ctx_execute` through stats, so none is skipped. `TestKnownDiffsListsKeptNotMeasured` pins the entry, as `TestKnownDiffsListsPythonCounters` does.

## 5. Testing (TDD, red first)

- `store`: migration 4 on a v3 DB keeps rows with `unmeasured_calls` 0; `AddToolStatExec` adds it; `AddToolStat` adds 0; `Stats` sums it per tool and for the lifetime; fresh DB reaches version 4 (`TestSchemaVersion` compares to `len(migrations)`).
- `executor`: `Measured` true for JS, TS and Python (counters on); false for shell, Ruby, Python with `CMGO_PY_COUNTERS=0`, Background, `execute_file`.
- `mcpserver`: shell call then `ctx_stats` shows `· 1 not measured`; JS call shows no suffix; argument-error call adds nothing; a seeded legacy row prints no suffix; lifetime suffix present only with unmeasured calls.
- `stats.Format`: row suffix and lifetime suffix, present and absent.
- `gofmt`, `go vet`, `go test ./... -count=1`, `-race`, `cmgo-hook` deps unchanged (stats formatting only, no `executor` import).

## 6. Decisions taken by default

- One column, no `measured_calls`, no "unrecorded" label (owner): that label would show only in sessions that straddle the migration, and never in lifetime.
- Calls that never ran code add nothing to `unmeasured_calls`: counting them would inflate the number read as "shell share".
- No per-language breakdown in `ctx_stats`; the re-audit gets languages from transcripts.
