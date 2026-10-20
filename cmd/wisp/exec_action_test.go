package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Re-exec tests use only this test binary and local files, never a real menu,
// terminal, compositor, notification command, or daemon.
func TestExecActionHelper(t *testing.T) {
	if os.Getenv("WISP_TEST_EXEC_HELPER") != "1" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(90)
	}
	switch args[0] {
	case "_exec_supervise":
		if err := superviseExec(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "wisp exec supervisor: %v\n", err)
			os.Exit(1)
		}
	case "caller":
		var cfg envConfig
		// envConfig has private fields, so construct it from a small test payload.
		var input struct{ Self, Log, Dir string }
		if json.Unmarshal([]byte(os.Getenv("WISP_TEST_EXEC_CALLER")), &input) != nil {
			os.Exit(91)
		}
		cfg.self, cfg.logPath, cfg.backend = input.Self, input.Log, "must-not-select-backend"
		inv := invocation{Mode: "exec", ID: "direct", Command: input.Self, WorkDir: input.Dir, Args: []string{"worker", input.Dir, "literal $(not-a-shell) ' \n雪"}}
		if err := launchInvocation(cfg, inv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(92)
		}
	case "worker":
		if len(args) != 3 {
			os.Exit(93)
		}
		dir, _ := os.Getwd()
		// Reading stdin must see /dev/null, not consume the palette's input.
		_, stdinErr := os.Stdin.Read(make([]byte, 1))
		info, _ := json.Marshal(struct {
			Dir, Arg string
			Group    int
			StdinEOF bool
		}{dir, args[2], syscall.Getpgrp(), errors.Is(stdinErr, io.EOF)})
		if os.WriteFile(filepath.Join(args[1], "started"), info, 0o600) != nil {
			os.Exit(94)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(args[1], "release")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(95)
			}
			time.Sleep(10 * time.Millisecond)
		}
		// More than a pipe buffer on each stream: logging cannot depend on a
		// live caller draining pipes (or waiting for the menu to close).
		fmt.Fprint(os.Stdout, strings.Repeat("exec stdout marker\n", 8192))
		fmt.Fprint(os.Stderr, strings.Repeat("exec stderr marker\n", 8192))
		_ = os.WriteFile(filepath.Join(args[1], "finished"), []byte("done"), 0o600)
		os.Exit(7)
	case "success":
		fmt.Fprintln(os.Stdout, "successful exec output")
	default:
		os.Exit(96)
	}
	os.Exit(0)
}

func execFixture(t *testing.T) (envConfig, string) {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(dir, "wisp-test")
	script := "#!/bin/sh\nexec " + shellQuote(binary) + " -test.run=^TestExecActionHelper$ -- \"$@\"\n"
	if err := os.WriteFile(self, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hyprctl", "kitty", "aerospace", "open"} {
		trap := "#!/bin/sh\nprintf forbidden > " + shellQuote(filepath.Join(dir, "forbidden")) + "\nexit 99\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(trap), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "exec-must-not-contact")
	t.Setenv("WISP_TEST_EXEC_HELPER", "1")
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	cfg := envConfig{self: self, backend: "hyprland", logPath: filepath.Join(dir, "exec.log")}
	return cfg, dir
}

func awaitExecFile(t *testing.T, path, contains string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), contains) {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s containing %q: %v; content=%q", path, contains, err, data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func releaseExec(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecSurvivesShortLivedCaller(t *testing.T) {
	cfg, dir := execFixture(t)
	input, _ := json.Marshal(struct{ Self, Log, Dir string }{cfg.self, cfg.logPath, dir})
	t.Setenv("WISP_TEST_EXEC_CALLER", string(input))
	caller := exec.Command(cfg.self, "caller")
	caller.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if out, err := caller.CombinedOutput(); err != nil {
		t.Fatalf("caller: %v: %s", err, out)
	}
	// The launcher has now exited while the worker deliberately remains open.
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600) })
	data := awaitExecFile(t, filepath.Join(dir, "started"), "StdinEOF")
	var info struct {
		Dir, Arg string
		Group    int
		StdinEOF bool
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Dir != dir || !info.StdinEOF || info.Arg != "literal $(not-a-shell) ' \n雪" {
		t.Fatalf("lost exec context: %+v", info)
	}
	if info.Group == caller.Process.Pid || info.Group == syscall.Getpgrp() {
		t.Fatalf("worker retained caller process group: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, "finished")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("caller waited for worker lifetime")
	}
	releaseExec(t, dir)
	awaitExecFile(t, cfg.logPath, "exit status 7")
	log, _ := os.ReadFile(cfg.logPath)
	for _, text := range []string{`action="direct"`, "exec stdout marker", "exec stderr marker", "failed: exit status 7"} {
		if !strings.Contains(string(log), text) {
			t.Fatalf("missing %q in exec log", text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "forbidden")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("exec contacted a window backend")
	}
}

func TestExecCatalogBypassesBackend(t *testing.T) {
	cfg, dir := execFixture(t)
	cfg.actionsPath = filepath.Join(dir, "actions.toml")
	catalog := "[[action]]\nid = \"controls\"\nkind = \"exec\"\ncommand = [" + strconv.Quote(cfg.self) + ", \"worker\", " + strconv.Quote(dir) + ", \"argument\"]\n"
	if err := os.WriteFile(cfg.actionsPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600) })
	// Exercise the real daemon handler without starting/stopping a daemon.
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { handleDaemonConn(cfg, server); close(done) }()
	if err := json.NewEncoder(client).Encode(daemonRequest{Op: "action", Action: "controls"}); err != nil {
		t.Fatal(err)
	}
	var response daemonResponse
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	<-done
	if !response.OK {
		t.Fatal(response.Message)
	}
	data := awaitExecFile(t, filepath.Join(dir, "started"), "StdinEOF")
	var info struct{ Group int }
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "finished")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("waited for exec completion")
	}
	releaseExec(t, dir)
	awaitExecFile(t, cfg.logPath, "exit status 7")
	// The worker was reaped by its supervisor before the error was logged;
	// the daemon's wait goroutine must reap the supervisor as well.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(info.Group, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("exec supervisor %d was not reaped", info.Group)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "forbidden")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("exec used hyprctl/kitty")
	}
}

func TestExecSuccessfulCompletion(t *testing.T) {
	cfg, dir := execFixture(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, cfg.self)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit relative commands resolve against the caller, not inv.WorkDir.
	if err := launchInvocation(cfg, invocation{Mode: "exec", ID: "success", Command: relative, WorkDir: dir, Args: []string{"success"}}); err != nil {
		t.Fatal(err)
	}
	data := awaitExecFile(t, cfg.logPath, "completed")
	if !strings.Contains(string(data), "successful exec output") {
		t.Fatal("lost stdout from successful exec")
	}
}

func TestExecValidationAndStartErrors(t *testing.T) {
	for _, failure := range []string{"missing command", "invalid directory", "not executable", "supervisor start", "log open"} {
		t.Run(failure, func(t *testing.T) {
			cfg, dir := execFixture(t)
			inv := invocation{Mode: "exec", ID: "bad", Command: cfg.self, WorkDir: dir}
			want := ""
			switch failure {
			case "missing command":
				inv.Command = "wisp-nonexistent-command"
				want = "command not found"
			case "invalid directory":
				inv.WorkDir = filepath.Join(dir, "missing")
				want = "working directory does not exist"
			case "not executable":
				inv.Command = filepath.Join(dir, "not-executable")
				if err := os.WriteFile(inv.Command, []byte("no"), 0o600); err != nil {
					t.Fatal(err)
				}
				want = "start exec action"
			case "supervisor start":
				cfg.self = filepath.Join(dir, "missing-supervisor")
				want = "start exec supervisor"
			case "log open":
				cfg.logPath = dir
				want = "open exec log"
			}
			if err := launchInvocation(cfg, inv); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %q, got %v", want, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "forbidden")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("exec validation contacted backend")
			}
		})
	}
}
