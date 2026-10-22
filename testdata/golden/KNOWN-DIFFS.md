# Known differences: cmgo vs TS golden output

The golden files in this directory are TS output (`tools/golden-gen/gen.mjs`,
TS commit `a5e6dd31`). Spec success criterion 3 allows only the differences
listed here. Each one has an ID and the golden case IDs it touches. The Go
tests skip or normalize exactly these cases and fail if a listed difference
goes away (so this list cannot go stale silently).

Case ID forms: `chunks:<path>`, `terms:<path>`, `sanitize:<query>`,
`search:qNN`, `snippet:qNN#i@max` (store); `tNN` (tools.json step);
`html:<path>` (fetch).

## Bugs (parity bugs, not by design)

These cases assert the **current Go output**, pinned in the test, until the
bug is fixed or the spec is changed. When the Go code changes, update the pin
and this list together.

| ID | Cases | Test | Description |
|---|---|---|---|

## By design: Turndown vs html-to-markdown v2 (spec §5.2)

Applied by `internal/fetch/golden_test.go` (`htmlNormalizers`, `htmlKnown`)
to both outputs, in the order listed per case:
`html:html/strip.html` = title, strike, list, escape;
`html:html/table.html` = title, list, table-delim, table-headless.

| ID | Cases | Turndown (TS) | html-to-markdown (cmgo) |
|---|---|---|---|
| `html-title` | `html:html/strip.html`, `html:html/table.html` | Keeps the `<head><title>` text as the first line | Drops `<head>` entirely |
| `html-strike` | `html:html/strip.html` | `~legacy~` | `~~legacy~~` |
| `html-list` | `html:html/strip.html`, `html:html/table.html` | Markers padded to 4 columns (`*   x`, `1.  x`), nested lists indented by 4 | `* x`, `1. x`, nested by 2, plus a whitespace-only line before a nested list |
| `html-escape` | `html:html/strip.html` | Decodes `&lt;` `&gt;` `&amp;`; escapes `\*` and `\]` | Keeps `&lt;Pool&gt;`; leaves `*` and `]` unescaped (both escape `[` and `_`) |
| `html-table-delim` | `html:html/table.html` | Delimiter row `\| --- \| --- \|` | `\|---\|---\|` |
| `html-table-headless` | `html:html/table.html` | A `<table>` without `<th>` stays raw HTML | Rendered as a Markdown table with an empty header row |

Everything else in both pages matches: ATX headings, fenced code with
language, `**bold**`, `_em_`, links with titles, images, `* * *` rules,
hard line breaks, blockquotes, and removal of script/style/nav/header/footer/noscript.

## By design: spec §2.2

No golden case differs because of a §2.2 item, so none is skipped:

- Batch `NODE_OPTIONS` fs preload: batch commands in `tools.json`
  never start `node`, so no `__CM_FS__` line appears on either side.
- `executor:python-counters`: `ctx_execute` counts the bytes a Python run reads through
  `open()`/pathlib and receives on sockets (TS counts JS/TS only). No golden case runs
  Python through stats, so none is skipped. Spec: `2026-10-22-cmgo-python-sandbox-bytes-design.md`.
- `stats:kept-not-measured`: the `ctx_stats` `ctx_execute` row ends with `· N not measured`,
  and the `All projects` line has `· N ctx_execute calls not measured` (`call` when N is 1), when `ctx_execute` ran
  code cmgo cannot count (shell and the other languages, detached background runs; TS has no
  such field). No golden case runs such a call through stats, so none is skipped. Spec:
  `2026-10-22-cmgo-kept-not-measured-design.md`.
- `stats:lifetime-split`: the `All projects` line of `ctx_stats` and the statusline `lifetime`
  show measured sandbox bytes only (`stats_totals`); hook-estimated `bytes_avoided` is no longer
  added to them. The session line `Redirected by hooks` excludes Read tips (the Read still runs)
  and ends with `(estimated)`. No golden case reads these lines, so none is skipped. Spec:
  `2026-10-22-cmgo-stats-measured-vs-estimated-design.md`.
- Timeline sort (`t11`): differs on purpose, session memory spec §3.2. TS
  keeps the best ranked matches and sorts them oldest first; cmgo ranks up to
  200 matches, keeps the newest by `indexed_at` (ties: the later chunk) and
  returns them oldest first. `t11` is soft-capped to 1 result per query and
  all its chunks share one `indexed_at`, so cmgo returns the last-indexed
  match (`batch:changelog,oversized`, Incident Report > Raw notes), TS the best
  ranked (`batch:getting started,troubleshooting,unicode`). The Go output is
  pinned in `goldenToolKnown`.
- Fetch transport, one-DB layout, no mmap, busy handling, dropped platforms:
  not observable in these outputs.

## Not compared

- `tools.json` masks (stored in the file, applied on both sides): dates
  `YYYY-MM-DD HH:MM`, `<n>ms`, `<n.n>KB`, and the `search calls in <n>s`
  window. KB sizes were checked once unmasked and matched.
- HTML chunks: only the HTML → Markdown text is compared; its chunks follow
  from the Markdown chunker, which `chunks.json` covers.
