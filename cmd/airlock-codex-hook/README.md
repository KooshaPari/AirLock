# AIRLOCK Codex PostToolUse observer

This standalone command reads one Codex hook event from stdin and best-effort
forwards a metadata-only `PostToolUse` record to a Unix socket. The emitted JSON
contains only `hook_event_name`, `session_id`, and `tool_name`; it excludes
`tool_input`, `tool_response`, and all unrecognized input fields. Input larger
than 1 MiB is rejected, and socket connect/write time is bounded to 250 ms.
Malformed input and observer failures return normally so telemetry cannot block
the tool flow.

Set `AIRLOCK_CODEX_HOOK_SOCKET` to select the socket; `AIRLOCK_SOCKET` is a
fallback, followed by `~/.airlock/codex-hook.sock`. The metadata-only socket
protocol is new and unverified against any receiver. The legacy installed
observer's wire contract and live Codex hook installation remain unverified;
this source addition does not wire or install the hook.
