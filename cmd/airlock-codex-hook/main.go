package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const rpcTimeout = 250 * time.Millisecond

type event struct {
	SessionID string          `json:"session_id,omitempty"`
	HookEventName string     `json:"hook_event_name,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
}

func socketPath() string {
	if path := os.Getenv("AIRLOCK_CODEX_HOOK_SOCKET"); path != "" { return path }
	if path := os.Getenv("AIRLOCK_SOCKET"); path != "" { return path }
	return filepath.Join(os.Getenv("HOME"), ".airlock", "codex-hook.sock")
}

func notify(path string, payload []byte) {
	defer func() { _ = recover() }()
	if path == "" || len(payload) == 0 { return }
	conn, err := net.DialTimeout("unix", path, rpcTimeout)
	if err != nil { return }
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(rpcTimeout))
	_, _ = conn.Write(append(payload, '\n'))
}

func run(input io.Reader, path string) {
	defer func() { _ = recover() }()
	data, err := io.ReadAll(io.LimitReader(input, 1<<20))
	if err != nil || len(data) == 0 { return }
	var ev event
	if json.Unmarshal(data, &ev) != nil || ev.HookEventName != "PostToolUse" { return }
	clean, err := json.Marshal(ev)
	if err != nil { return }
	notify(path, clean)
}

func main() { run(os.Stdin, socketPath()) }
