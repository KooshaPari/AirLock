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
const maxEventBytes = 1 << 20

func socketPath() string {
	if path := os.Getenv("AIRLOCK_CODEX_HOOK_SOCKET"); path != "" {
		return path
	}
	if path := os.Getenv("AIRLOCK_SOCKET"); path != "" {
		return path
	}
	return filepath.Join(os.Getenv("HOME"), ".airlock", "codex-hook.sock")
}

func notify(path string, payload []byte) {
	defer func() { _ = recover() }()
	if path == "" || len(payload) == 0 {
		return
	}
	conn, err := net.DialTimeout("unix", path, rpcTimeout)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(rpcTimeout))
	_ = writeAll(conn, append(append([]byte(nil), payload...), '\n'))
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func run(input io.Reader, path string) {
	defer func() { _ = recover() }()
	data, err := io.ReadAll(io.LimitReader(input, maxEventBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxEventBytes {
		return
	}
	var inputEvent struct {
		HookEventName string `json:"hook_event_name"`
		SessionID     string `json:"session_id"`
		ToolName      string `json:"tool_name"`
	}
	if json.Unmarshal(data, &inputEvent) != nil || inputEvent.HookEventName != "PostToolUse" {
		return
	}
	metadata, err := json.Marshal(struct {
		HookEventName string `json:"hook_event_name"`
		SessionID     string `json:"session_id"`
		ToolName      string `json:"tool_name"`
	}{inputEvent.HookEventName, inputEvent.SessionID, inputEvent.ToolName})
	if err != nil {
		return
	}
	notify(path, metadata)
}

func main() { run(os.Stdin, socketPath()) }
