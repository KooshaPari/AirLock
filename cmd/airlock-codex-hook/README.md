# AIRLOCK Codex PostToolUse observer

This standalone command reads one Codex hook event from stdin and best-effort
forwards valid `PostToolUse` JSON to a Unix socket. It preserves the full event,
rejects input larger than 1 MiB, and bounds socket connect/write time to 250 ms.
Malformed input and observer failures return normally so telemetry cannot block
the tool flow.

Set `AIRLOCK_CODEX_HOOK_SOCKET` to select the socket; `AIRLOCK_SOCKET` is a
fallback, followed by `~/.airlock/codex-hook.sock`. The legacy installed
observer's wire contract and live Codex hook installation are unverified and
are not implemented by this source addition.
