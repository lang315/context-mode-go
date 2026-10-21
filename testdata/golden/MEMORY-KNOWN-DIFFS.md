# Known differences: cmgo session memory vs TS golden output

The `memory-*.json` files in this directory come from
`tools/golden-gen/gen-memory.mjs` (TS commit `a5e6dd31`). The search tool
name is `mcp__cmgo__ctx_search` (spec §2.4). cmgo does not build the TS
events markdown (spec §2.3), so there are no golden files for it.

Each directive case holds two texts:
- `ts` is what TS ships: `buildSessionDirective` gets the return value of
  `writeSessionEventsFile` (`hooks/sessionstart.mjs:205-206`).
- `expected` is what cmgo must print. The generator computes it with the same
  TS functions on fixed input: `user-prompt` rows become category `prompt`,
  `mcp` and `data` rows move to categories that no section reads,
  `created_at` becomes the zero-padded id, and the directive gets
  `groupEvents()` output.

The Go tests compare cmgo against `expected`, byte for byte, for every case.
They also check that `ts` differs from `expected` in exactly the cases listed
below (`knownDiffs` in `internal/memory/golden_test.go`): a listed difference
that goes away fails, and an unlisted one fails. `memory-autoinjection.json`
has no fix to apply, so it holds `expected` only and has no rows here.

## Fixes (spec §2.4)

| Fix | TS | cmgo |
|---|---|---|
| `user-prompt` | "Last Request" and `<continue_from>` read category `prompt`, which capture never writes (R3 headline 4) | they read the last `user-prompt` event |
| `leg-boundary` | the directive gets no leg boundary, so prior-leg subagents read as current (R3 headline 5) | an event is prior-leg when its id is lower than the id of the last `session_start` event |
| `no-mcp-data` | "MCP Tools Used" and "Data References" sections | left out: capture writes no `mcp` or `data` events (hooks spec §3.4) |
| order by id | `getSessionEvents` orders by `created_at` | rows are used in id order. Not exercised here: the generator feeds every fixture in id order, as cmgo reads them |

## Cases

Case IDs are `directive:<name>`, where `<name>` is the `name` field of a
case in `memory-directive.json` (`<fixture>/<source>`).

| Case ID | Fixes |
|---|---|
| `directive:full/compact` | `user-prompt` (Last Request, `<continue_from>`), `no-mcp-data` |
| `directive:full/resume` | `user-prompt` (Last Request), `no-mcp-data` |
| `directive:leg/resume` | `leg-boundary` |
| `directive:leg-tie/compact` | `leg-boundary` |
| `directive:trunc/compact` | `user-prompt` (Last Request cut at 297 + `...`, `<continue_from>`), `no-mcp-data` |
| `directive:trunc-exact/resume` | `user-prompt` (Last Request) |
| `directive:trunc-astral/compact` | `user-prompt` (Last Request, `<continue_from>`) |
| `directive:prompts/compact` | `user-prompt` (Last Request, `<continue_from>`) |
| `directive:prompts/resume` | `user-prompt` (Last Request) |
| `directive:mcp-data-only/resume` | `no-mcp-data` |
