package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Same wire contract as tests/wisp-prefix.lua: synchronous atomic publication
// of TOKEN + newline; cancel writes only newline. Go is strictly a reader.
func publishTestHintSession(t *testing.T, path, token string) {
	t.Helper()
	if err := os.WriteFile(path+".tmp", []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func configureSessionHint(t *testing.T, helper string) {
	t.Helper()
	t.Setenv("WISP_HINT_HELPER", helper)
	t.Setenv("WISP_HINT_COMMAND", "")
	t.Setenv("WISP_HINT_BACKEND", "native")
}

func readTestHintRecord(t *testing.T, cfg envConfig) hintProcessRecord {
	t.Helper()
	data, err := os.ReadFile(cfg.hintPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	var record hintProcessRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestHintSessionCLIContract(t *testing.T) {
	s, err := parseHintSession("show", []string{"--session", "abc-1", "--state", "/runtime/hint.session"})
	if err != nil || s.Token != "abc-1" || s.StatePath != "/runtime/hint.session" {
		t.Fatalf("%+v: %v", s, err)
	}
	if _, err := parseHintSession("close", []string{"--session", "abc-1"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--session", "abc-1"},
		{"--session", "abc-1", "--state", "relative"},
		{"--session", "", "--state", "/runtime/state"},
		{"--session", "abc\n1", "--state", "/runtime/state"},
		{"--bad", "abc-1"},
		{"--session"},
	} {
		if _, err := parseHintSession("show", args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

func TestHintSessionQueuedShowAfterCancel(t *testing.T) {
	cfg, helper := hintFixture(t, "printf started > \"$WISP_TEST_STARTED\"; printf 'READY\\n' >&3; exec sleep 30")
	configureSessionHint(t, helper)
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("WISP_TEST_STARTED", marker)
	state := filepath.Join(t.TempDir(), "hint.session")
	session := &hintSession{Token: "abc-1", StatePath: state}
	publishTestHintSession(t, state, session.Token)
	done, queued := make(chan error, 1), make(chan struct{})
	// Hold the lock while the older show is scheduled and then canceled. Lock
	// acquisition/process startup order is explicitly NOT the generation order.
	err := withHintLock(cfg, func() error {
		go func() { close(queued); done <- showHintForSession(cfg, "test", session) }()
		<-queued
		publishTestHintSession(t, state, "")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := closeHintForSession(cfg, session.Token); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("canceled show launched helper: %v", err)
	}
	if _, err := os.Stat(cfg.hintPIDPath); !os.IsNotExist(err) {
		t.Fatalf("orphan PID record: %v", err)
	}
}

func TestHintSessionOldCloseAndShowCannotTouchNewPanel(t *testing.T) {
	cfg, helper := hintFixture(t, "printf 'READY\\n' >&3; exec sleep 30")
	configureSessionHint(t, helper)
	defer closeHint(cfg)
	state := filepath.Join(t.TempDir(), "hint.session")
	old := &hintSession{Token: "abc-1", StatePath: state}
	newer := &hintSession{Token: "abc-2", StatePath: state}
	publishTestHintSession(t, state, old.Token)
	if err := runDirect(cfg, []string{"hint", "show", "--session", old.Token, "--state", state}); err != nil {
		t.Fatal(err)
	}
	publishTestHintSession(t, state, "") // Esc queues old close, but it is delayed.
	publishTestHintSession(t, state, newer.Token)
	if err := runDirect(cfg, []string{"palette", "show", "--session", newer.Token, "--state", state}); err != nil {
		t.Fatal(err)
	}
	before := readTestHintRecord(t, cfg)
	if before.Session != newer.Token {
		t.Fatalf("wrong generation: %+v", before)
	}
	if err := runDirect(cfg, []string{"palette", "close", "--session", old.Token}); err != nil {
		t.Fatal(err)
	}
	if err := showHintForSession(cfg, "old queued show", old); err != nil {
		t.Fatal(err)
	}
	after := readTestHintRecord(t, cfg)
	if before != after {
		t.Fatalf("old request replaced/unlinked new record: %+v -> %+v", before, after)
	}
	start, err := hintProcessStart(after.PID)
	if err != nil || start != after.Start {
		t.Fatalf("old close killed new process: %s %v", start, err)
	}
	if err := runDirect(cfg, []string{"palette", "close", "--session", newer.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.hintPIDPath); !os.IsNotExist(err) {
		t.Fatalf("current close failed: %v", err)
	}
}

func TestHintSessionInvalidationDuringNativeReadiness(t *testing.T) {
	cfg, helper := hintFixture(t, "printf '%s' $$ > \"$WISP_TEST_STARTED\"; IFS= read -r gate < \"$WISP_TEST_GATE\"; printf 'READY\\n' >&3; exec sleep 30")
	configureSessionHint(t, helper)
	defer closeHint(cfg)
	dir := t.TempDir()
	marker, gate := filepath.Join(dir, "started"), filepath.Join(dir, "gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WISP_TEST_STARTED", marker)
	t.Setenv("WISP_TEST_GATE", gate)
	state := filepath.Join(dir, "hint.session")
	session := &hintSession{Token: "abc-1", StatePath: state}
	publishTestHintSession(t, state, session.Token)
	done := make(chan error, 1)
	go func() { done <- showHintForSession(cfg, "test", session) }()
	deadline := time.Now().Add(2 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(marker)
		pid, _ = strconv.Atoi(string(data))
		if pid > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("helper did not reach readiness gate")
	}
	publishTestHintSession(t, state, "")
	// Nonblocking open avoids a stuck test if helper startup unexpectedly fails.
	var f *os.File
	var err error
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f, err = os.OpenFile(gate, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if !errors.Is(err, syscall.ENXIO) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("map\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.hintPIDPath); !os.IsNotExist(err) {
		t.Fatalf("canceled startup left PID record: %v", err)
	}
	if _, err := hintProcessStart(pid); !os.IsNotExist(err) {
		t.Fatalf("canceled startup child not reaped: %v", err)
	}
}

func TestHintSessionMissingOrMalformedStateFailsClosed(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	s := &hintSession{Token: "abc-1", StatePath: state}
	if s.current() {
		t.Fatal("missing state accepted")
	}
	for _, data := range []string{"", "abc-1", "abc-1\nextra", "abc-2\n", "\n"} {
		if err := os.WriteFile(state, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if s.current() {
			t.Fatalf("accepted state %q", data)
		}
	}
	publishTestHintSession(t, state, s.Token)
	if !s.current() {
		t.Fatal("valid state rejected")
	}
	if _, err := parseHintSession("text", strings.Fields("--session abc-1")); err == nil {
		t.Fatal("text accepted session options")
	}
}
