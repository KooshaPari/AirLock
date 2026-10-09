package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunForwardsOnlyMetadata(t *testing.T) {
	cwd := initRepo(t)
	path := socketTestPath(t, "hook.sock")
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
	input, err := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "cwd": cwd, "tool_input": map[string]string{"command": "token=secret-input"}, "tool_response": map[string]string{"output": "secret-response"}, "extra": "secret-extra"})
	if err != nil {
		t.Fatal(err)
	}
	run(bytes.NewReader(input), path)
	select {
	case payload := <-got:
		var got event
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		if got.Agent != "codex" || got.Hook != "Bash" || got.Repo != cwd || got.Branch != "main" || got.Dirty != 0 || got.TS == "" {
			t.Fatalf("metadata = %+v", got)
		}
		for _, secret := range [][]byte{[]byte("secret-input"), []byte("secret-response"), []byte("secret-extra"), []byte("tool_input"), []byte("tool_response"), []byte("session_id")} {
			if bytes.Contains(payload, secret) {
				t.Fatalf("forwarded payload contains private input %q: %s", secret, payload)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("hook did not notify observer")
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main", dir}, {"-C", dir, "config", "user.email", "test@example.com"}, {"-C", dir, "config", "user.name", "Test"}, {"-C", dir, "commit", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func socketTestPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ah")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func TestRepoMetadataTracksDirtyAndRejectsNonGit(t *testing.T) {
	cwd := initRepo(t)
	if _, _, dirty, ok := repoMetadata(cwd); !ok || dirty != 0 {
		t.Fatalf("clean repo metadata dirty=%v ok=%v", dirty, ok)
	}
	if err := os.WriteFile(filepath.Join(cwd, "dirty"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, branch, dirty, ok := repoMetadata(cwd)
	if !ok || repo != cwd || branch != "main" || dirty != 1 {
		t.Fatalf("dirty repo metadata = %q %q %d %v", repo, branch, dirty, ok)
	}
	if _, _, _, ok := repoMetadata(t.TempDir()); ok {
		t.Fatal("non-git cwd accepted")
	}
}

func TestRunSkipsMissingOrInvalidCWD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	for _, input := range []string{`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`, `{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":"relative"}`} {
		run(bytes.NewBufferString(input), path)
	}
}

func TestRunRejectsOversizedPayload(t *testing.T) {
	path := socketTestPath(t, "o.sock")
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
	path := socketTestPath(t, "s.sock")
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
