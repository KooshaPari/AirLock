package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const rpcTimeout = 250 * time.Millisecond
const maxEventBytes = 1 << 20
const gitWorkBudget = 350 * time.Millisecond
const maxGitOutput = 256 << 10
const maxGitPathOutput = 8 << 10

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, io.ErrShortWrite
	}
	return b.Buffer.Write(p)
}

func gitOutput(ctx context.Context, limit int, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout cappedBuffer
	stdout.limit = limit
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

type event struct {
	Agent  string `json:"agent"`
	Hook   string `json:"hook"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Dirty  int    `json:"dirty"`
	TS     string `json:"ts"`
}

func repoMetadata(cwd string) (repo, branch string, dirty int, ok bool) {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", "", 0, false
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return "", "", 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitWorkBudget)
	defer cancel()
	root, err := gitOutput(ctx, maxGitPathOutput, "-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", 0, false
	}
	repo = strings.TrimSpace(string(root))
	name, err := gitOutput(ctx, maxGitPathOutput, "-C", repo, "branch", "--show-current")
	if err != nil {
		return "", "", 0, false
	}
	branch = strings.TrimSpace(string(name))
	if branch == "" {
		return "", "", 0, false
	}
	status, err := gitOutput(ctx, maxGitOutput, "-C", repo, "status", "--porcelain", "-z")
	if err != nil {
		return "", "", 0, false
	}
	entries := 0
	rows := strings.Split(string(status), "\x00")
	for i := 0; i < len(rows); i++ {
		row := rows[i]
		if row == "" {
			continue
		}
		entries++
		if len(row) >= 2 && (row[0] == 'R' || row[0] == 'C' || row[1] == 'R' || row[1] == 'C') {
			i++
		}
	}
	return repo, branch, entries, true
}

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
		ToolName      string `json:"tool_name"`
		CWD           string `json:"cwd"`
	}
	if json.Unmarshal(data, &inputEvent) != nil || inputEvent.HookEventName != "PostToolUse" {
		return
	}
	repo, branch, dirtyCount, ok := repoMetadata(inputEvent.CWD)
	if !ok {
		return
	}
	metadata, err := json.Marshal(event{"codex", inputEvent.ToolName, repo, branch, dirtyCount, time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return
	}
	notify(path, metadata)
}

func main() { run(os.Stdin, socketPath()) }
