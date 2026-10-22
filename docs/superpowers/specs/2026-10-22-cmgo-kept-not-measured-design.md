# cmgo: show "not measured" for `ctx_execute` runs whose kept bytes cannot be counted (design)

Date: 2026-10-22. Rev 1. Extends stats spec §1.3 and §2 (`2026-10-21-cmgo-stats-design.md`).

## 0. Problem and goal

`ctx_execute` counts the bytes a run reads only for JS/TS (stderr counters) and Python (sandbox counters, PR #12). Shell and the compiled/other languages cannot be measured on macOS, so those calls record `bytes_kept = 0`. Shell is about 71% of `ctx_execute` calls. `ctx_stats` shows `0 B kept` for them, which reads as "no savings" when the truth is "unknown".

Goal: `ctx_stats` says which `ctx_execute` calls were not measured. Success:

1. A shell `ctx_execute` is stored as not measured, and the session report says so on the `ctx_execute` row.
2. Rows written before this change are never shown as measured.
3. Kept totals and percentages do not change.

Out of scope: measuring shell; changing the statusline (TS parity and its 10 ms budget); changing `ctx_execute_file`, `ctx_batch_execute`, `ctx_fetch_and_index` (always measured).

## 1. Decision: count columns on `tool_stats`, not a per-language split

Q1. How is "measured" recorded? Two new columns on `tool_stats`, migration 4 (`user_version` 3 → 4), appended to `migrations`:

```sql
ALTER TABLE tool_stats ADD COLUMN measured_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tool_stats ADD COLUMN unmeasured_calls INTEGER NOT NULL DEFAULT 0;
```

Both are summed like `calls`. Only `ctx_execute` writes them (other tools write 0, 0 and the report ignores their columns).

Why not a per-language split: the primary key is `(server_id, tool, window)`; adding language multiplies rows and changes the key (a table rebuild). Languages also mislead: Python with `CMGO_PY_COUNTERS=0`, a Background run or a failed hook setup is unmeasured too. The executor knows the truth, so it reports it.

Why two columns, not one `unmeasured_calls`: with one column, old rows (default 0) look like "all measured". With both, `unknown = calls - measured_calls - unmeasured_calls` is exactly the calls recorded before this change (or dropped counts), and is never relabeled.

## 2. Recording

- `executor.Result` gets `Measured bool`: true when the run was instrumented and its counters were read (the `wrapJS` path, or the Python counters file path). False for every other run, including Python with counters off, Background, `execute_file` and compile failures.
- `ctx_execute` classifies each call:
  - `Run` returned and `!Measured` → unmeasured.
  - Everything else (a measured run, or a call that never ran code: argument error, security denial, runtime-missing error) → measured. Nothing was left to measure, so `kept 0` is true.
- `StatsRecorder` gets one more entry, `RecordExec(d, tool, raw, res, unmeasured bool)`, with `Record` unchanged for the other tools. `statJob` carries a `measured`/`unmeasured` 0/1 pair, and `AddToolStat` takes them and adds them in the same upsert. `stats_totals` is not changed (it has no per-tool data).
- A timeout or `os._exit` Python run counts as measured (the executor had instrumented it; the lower bound is already documented in the Python spec §5). Only runs the executor never instrumented are unmeasured.

## 3. Display

Q2. `ctx_stats`:

- **Session** (per-tool rows, `window`/`server_id` filter as today): the `ctx_execute` row gets a suffix only when needed:

```
  ctx_execute            20 calls    1.2 KB kept   3.0 KB returned  ·  14 not measured
  ctx_execute            20 calls    1.2 KB kept   3.0 KB returned  ·  14 not measured, 3 unrecorded
```

  `unrecorded` appears only when `unknown > 0`, which is calls from before migration 4 (or a rolled-back count). No suffix when both are 0, so a JS-only session prints exactly as before.
- **Project:** the report has no kept figure per project (stats spec §2), so there is nothing to annotate. Not changed.
- **Lifetime** (`All projects` line): `kept out` is a lower bound. When any `ctx_execute` call in `tool_stats` is unmeasured, the line gets ` · N ctx_execute calls not measured` after the kept figure (before `sessions`). Computed from `SUM(unmeasured_calls)` over `tool_stats` (all projects), one query added to the existing `Stats` select.
- Headline `Kept out of context` and `pct`: unchanged.
- Statusline: unchanged.

Q3. Backward compatibility. Existing rows keep `measured_calls = unmeasured_calls = 0`. Nothing rewrites them. They show as `unrecorded`, never as measured. Lifetime "not measured" counts only recorded unmeasured calls, so it does not claim to know the old ones. Whether an old row's shell calls were unmeasured is exactly what was lost.

TS-imported rows: `tsimport` does not write `tool_stats` (checked: no reference in `internal/tsimport`), so no imported row exists. If a future import adds some, they fall in `unrecorded`.

## 4. KNOWN-DIFFS

Q4. TS has no such field. Add `stats:kept-not-measured` to `testdata/golden/KNOWN-DIFFS.md`: the `ctx_stats` per-tool row and the `All projects` line may carry a "not measured" suffix. No golden case runs a non-JS/TS/Python `ctx_execute` through stats, so none is skipped. `TestKnownDiffsListsKeptNotMeasured` pins the entry, as `TestKnownDiffsListsPythonCounters` does.

## 5. Testing (TDD, red first)

- `store`: migration 4 on a v3 DB with rows keeps them with 0, 0; `AddToolStat` adds both columns; `Stats` sums them; fresh DB reaches version 4 (`TestSchemaVersion` updated in the same change).
- `executor`: `Measured` true for JS, TS and Python (counters on); false for shell, Ruby, Python with `CMGO_PY_COUNTERS=0`, Background, `execute_file`.
- `mcpserver`: shell call then `ctx_stats` shows `· 1 not measured`; JS call shows no suffix; argument-error call counts as measured; a seeded legacy row shows `unrecorded`; lifetime suffix present only with unmeasured calls.
- `stats.Format`: suffix variants and padding.
- `gofmt`, `go vet`, `go test ./... -count=1`, `-race`, `cmgo-hook` deps unchanged (stats formatting only, no `executor` import).

## 6. Decisions taken by default

- `unrecorded` is the label for pre-change calls (not "unknown"): it says the cause, not a state of the call.
- Early-error calls count as measured: counting them as unmeasured would inflate the number the owner reads as "shell share".
- No per-language breakdown in `ctx_stats`; the re-audit (task 3) gets languages from transcripts.
