# cmgo audit

Measures whether cmgo is effective: bytes kept out of context, adoption, hook redirects, the Bash read guidance rollback rule (PR #11 spec §5.3), context growth, hook latency and failures. Definitions and the verdict rules are in `METHOD.md`; every number in the report is printed by the script.

Read-only on `~/.claude`: the cmgo DB and its WAL are copied to a temp dir and opened there, transcripts are only read.

## Re-run

```sh
python3 -I tools/audit/cmgo_audit.py
```

About 15 s with a warm cache, a few minutes the first time. Output goes to `~/.cache/cmgo-audit/` (`report.md`, `report.json`, and the caches), or `$CMGO_AUDIT_OUT`, or `--out DIR`. Nothing is written inside the repo. `--since YYYY-MM-DD` changes the window (default 2026-09-30).

`subagent_probe.py` is the per-subagent part (also runnable alone, it writes `subagent_probe.json`).

`python3 -I tools/audit/compact_audit.py [--since YYYY-MM-DD]`: Bash compaction M1-M3 and the keep/raise/remove verdict (bash-compact spec §5).

## Sample-size gate

A question is "not enough data" when its post-install sample is under 30 events or the window is under 3 days. The last section of `report.md` lists each gate with its n. Subagent routing (option O1 in `docs/superpowers/specs/2026-10-22-cmgo-subagent-routing-design.md`) is decided on the cmgo row of the subagent funnel, which needs at least 60 subagents with the block and the tools listed.

## Dates and constants inside the script

`ERA_CMGO` (2026-10-21T08:00Z, first cmgo `tool_stats` row), `PY_COUNTERS` (Python counters installed, 2026-10-22 05:36Z), `UNMEAS_COL` (migration 4, 2026-10-22 06:30Z), `TEST_WINDOW` (the 2.79 MB test call of session `5f9ecd0a`). Test projects are those whose directory name contains `scratchpad` or `-e2e`, or starts with `-private-tmp`.
