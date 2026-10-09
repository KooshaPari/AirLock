package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunSendsPostToolUseEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hook.sock")
	listener, err := net.Listen("unix", path)
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	got := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil { return }
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		got <- bytes.TrimSpace(buf[:n])
	}()
	run(bytes.NewBufferString(`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash"}`), path)
	select {
	case payload := <-got:
		if !bytes.Contains(payload, []byte(`"hook_event_name":"PostToolUse"`)) { t.Fatalf("unexpected payload: %s", payload) }
	case <-time.After(time.Second): t.Fatal("hook did not notify observer")
	}
}

func TestRunIgnoresInvalidAndOtherEvents(t *testing.T) {
	// A nonexistent socket is a negative control: malformed/non-target input
	// and observer failures must all return normally without creating one.
	path := filepath.Join(t.TempDir(), "absent.sock")
	for _, input := range []string{"not json", `{"hook_event_name":"PreToolUse"}`, `{"hook_event_name":"PostToolUse"}`} {
		run(bytes.NewBufferString(input), path)
	}
}

func TestNotifyReturnsWhenSocketMissing(t *testing.T) {
	start := time.Now()
	notify(filepath.Join(t.TempDir(), "absent.sock"), []byte(`{}`))
	if time.Since(start) > time.Second { t.Fatal("observer failure exceeded bounded return time") }
}

func TestSocketOverride(t *testing.T) {
	t.Setenv("AIRLOCK_CODEX_HOOK_SOCKET", "/tmp/override.sock")
	if got := socketPath(); got != "/tmp/override.sock" { t.Fatalf("socketPath() = %q", got) }
	_ = os.Unsetenv("AIRLOCK_CODEX_HOOK_SOCKET")
}
