# Known differences: cmgo hooks vs TS golden output

The `hooks-*.json` files in this directory are TS output
(`tools/golden-gen/gen-hooks.mjs`, TS commit `a5e6dd31`), with every
`mcp__plugin_context-mode_context-mode__` replaced by `mcp__cmgo__`
(spec §1.3). Spec §6.2 allows only the differences listed here. The Go tests
hold the same rows (`knownDiffs` in `internal/routing/golden_test.go`,
`fixed` and `dropped` in `internal/capture/golden_test.go`). They fail if a
listed difference goes away, and they fail if any unlisted case differs.

Case ID forms: `pretooluse:<i>` (the `i` field of a case in
`hooks-pretooluse.json`); `capture:<name>` (the `name` field of a tool case
in `hooks-capture.json`); `strings:<name>` (a difference in the
`hooks-strings.json` texts, applied by `TestGoldenStrings`); `routing:<name>`
(an extension with no TS case to differ from, so no golden case lists it;
`TestKnownDiffsRowExists` in `internal/routing/replay_test.go` requires the
row and checks that the extension changes the decision).

## Cases

| Case ID | File | Reason |
|---|---|---|
| `pretooluse:25` | `hooks-pretooluse.json` | spec §2.3: `ctx_execute` is not a PreToolUse matcher, so cmgo writes nothing (TS pins `cwd`) |
| `pretooluse:26` | `hooks-pretooluse.json` | spec §2.3: `ctx_execute` is not a PreToolUse matcher; `cmgo serve` checks deny rules itself (TS denies `sudo ls` here) |
| `pretooluse:28` | `hooks-pretooluse.json` | spec §2.3: `ctx_execute_file` is not a PreToolUse matcher; `cmgo serve` checks deny rules itself (TS denies the `.env` path here) |
| `pretooluse:29` | `hooks-pretooluse.json` | spec §2.3: `ctx_batch_execute` is not a PreToolUse matcher; `cmgo serve` checks deny rules itself (TS denies `sudo id` here) |
| `pretooluse:30` | `hooks-pretooluse.json` | spec §2.3: `ctx_batch_execute` is not a PreToolUse matcher, so cmgo writes nothing (TS pins `cwd`) |
| `pretooluse:31` | `hooks-pretooluse.json` | spec §2.3: external-MCP guidance dropped; the TS `mcp__` matcher never fires in Claude Code (R1 P11) |
| `capture:git -C checkout (TS bug)` | `hooks-capture.json` | spec §3.4: the parsed git subcommand is matched, so cmgo records `cwd` and `git branch` (TS records neither) |
| `strings:ctx-purge` | `hooks-strings.json` (routing blocks with `includeCommands`) | The `ctx purge` action asks whether to purge one session or the whole project and warns before calling `ctx_purge`, as the `/ctx-purge` skill does. TS says `Call purge MCP tool with confirm: true. Warn: irreversible.`, which lets the model delete the whole project without asking. |
| `strings:ctx-doctor` | `hooks-strings.json` (routing blocks with `includeCommands`) | The `ctx doctor` action shows the report that `ctx_doctor` returns, with its fix hints. TS says `Call doctor MCP tool, run returned shell command, display as checklist.`, but cmgo's `ctx_doctor` runs its checks itself and returns no command. |
| `routing:bash-read-guidance` | (no TS case) | A Bash command that reads files of at least `CMGO_BASH_READ_MIN_BYTES` (off unless set, since 2026-10-27; `16384` restores the old default) with `cat`, `nl`, `tac`, `bat`, `head` or `tail` gets a context hint pointing at `ctx_execute_file` / `ctx_batch_execute` (and at Read for editing); spec `2026-10-21-cmgo-bash-read-guidance-design.md`. TS only gives the generic Bash tip. |
| `capture:Read CLAUDE.md CC object response` | `hooks-capture.json` | spec §3.4: `rule_content` stores `tool_response.file.content` (TS stores the stringified response object) |

## Dropped event types (applied to every capture case)

spec §3.4: cmgo does not port these extractors, so the capture test removes
these types from every TS output before comparing: `error_tool` (moved to
PostToolUseFailure), `constraint_discovered`, `bash_outcome`,
`file_read_metadata`, `agent_finding`, `external_ref`, `mcp`,
`mcp_tool_call`, `webfetch_metadata`, `worktree_exit`, `agent_usage`,
`error_resolved`, `retry_detected`, and the UserPromptSubmit `data` event.
No TS output in `hooks-capture.json` contains `webfetch_metadata`,
`worktree_exit`, `agent_usage`, `error_resolved` or `retry_detected`, so
for those five the list is not exercised.
