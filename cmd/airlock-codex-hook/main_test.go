package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestRunPreservesFullPostToolUsePayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hook.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	got := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		gotBytes, _ := io.ReadAll(conn)
		got <- bytes.TrimSuffix(gotBytes, []byte{'\n'})
	}()
	input := []byte(`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo secret"},"tool_response":{"output":"done"},"extra":true}`)
	run(bytes.NewReader(input), path)
	select {
	case payload := <-got:
		if !bytes.Equal(payload, input) {
			t.Fatalf("payload changed\n got: %s\nwant: %s", payload, input)
		}
	case <-time.After(time.Second):
		t.Fatal("hook did not notify observer")
	}
}

func TestRunRejectsOversizedPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
			accepted <- struct{}{}
		}
	}()
	input := append([]byte(`{"hook_event_name":"PostToolUse","padding":"`), bytes.Repeat([]byte{'x'}, maxEventBytes)...)
	input = append(input, []byte(`"}`)...)
	run(bytes.NewReader(input), path)
	select {
	case <-accepted:
		t.Fatal("oversized event was forwarded")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestRunIgnoresInvalidAndOtherEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	for _, input := range []string{"not json", `{"hook_event_name":"PreToolUse"}`, `{"hook_event_name":"PostToolUse"}`} {
		run(bytes.NewBufferString(input), path)
	}
}

func TestNotifyReturnsWhenSocketMissing(t *testing.T) {
	start := time.Now()
	notify(filepath.Join(t.TempDir(), "absent.sock"), []byte(`{}`))
	if time.Since(start) > time.Second {
		t.Fatal("observer failure exceeded bounded return time")
	}
}

func TestNotifyTimesOutWhenPeerDoesNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stalled.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			close(accepted)
			<-time.After(time.Second)
		}
	}()
	start := time.Now()
	notify(path, bytes.Repeat([]byte{'x'}, 8<<20))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stalled peer held hook for %s", elapsed)
	}
	select {
	case <-accepted:
	default:
		t.Fatal("peer was not accepted")
	}
}

type shortWriter struct{ calls int }

func (w *shortWriter) Write(p []byte) (int, error) {
	w.calls++
	if len(p) > 2 {
		return 2, nil
	}
	return len(p), nil
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
func TestWriteAllHandlesShortAndZeroWrites(t *testing.T) {
	w := &shortWriter{}
	if err := writeAll(w, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if w.calls < 2 {
		t.Fatal("writeAll did not retry short write")
	}
	if err := writeAll(zeroWriter{}, []byte("payload")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero write error = %v", err)
	}
}

func TestSocketPathOverridesAndHomeFallback(t *testing.T) {
	t.Setenv("AIRLOCK_CODEX_HOOK_SOCKET", "/tmp/override.sock")
	if got := socketPath(); got != "/tmp/override.sock" {
		t.Fatalf("socketPath() = %q", got)
	}
	t.Setenv("AIRLOCK_CODEX_HOOK_SOCKET", "")
	t.Setenv("AIRLOCK_SOCKET", "/tmp/legacy.sock")
	if got := socketPath(); got != "/tmp/legacy.sock" {
		t.Fatalf("socketPath() = %q", got)
	}
	t.Setenv("AIRLOCK_SOCKET", "")
	t.Setenv("HOME", "/tmp/home")
	if got := socketPath(); got != "/tmp/home/.airlock/codex-hook.sock" {
		t.Fatalf("socketPath() = %q", got)
	}
}
