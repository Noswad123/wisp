package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Hint operations use a separate cross-process lock, never the overlay lock.
func withHintLock(cfg envConfig, fn func() error) error {
	if runtime.GOOS != "linux" {
		return fn()
	}
	path := expand(cfg.hintPIDPath) + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

type hintProcessRecord struct {
	PID     int    `json:"pid"`
	Start   string `json:"start_time"`
	Native  bool   `json:"native"`
	Session string `json:"session,omitempty"`
}

func hintProcessStart(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid hint PID")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	// comm can contain spaces and parentheses; fields after its last ')' start at field 3.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return "", errors.New("incomplete process stat")
	}
	return fields[19], nil
}

func writeLinuxHintPID(cfg envConfig, pid int, native bool) error {
	return writeLinuxHintSessionPID(cfg, pid, native, nil)
}

func writeLinuxHintSessionPID(cfg envConfig, pid int, native bool, session *hintSession) error {
	start, err := hintProcessStart(pid)
	if err != nil {
		return err
	}
	data, err := json.Marshal(hintProcessRecord{PID: pid, Start: start, Native: native, Session: session.token()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(expand(cfg.hintPIDPath)), 0o700); err != nil {
		return err
	}
	return os.WriteFile(expand(cfg.hintPIDPath), data, 0o600)
}

func closeLinuxHint(data []byte) error {
	var record hintProcessRecord
	// Raw legacy PIDs cannot establish ownership: discard them without signaling.
	if json.Unmarshal(data, &record) != nil || record.Start == "" || record.PID <= 0 {
		return nil
	}
	proc, err := os.FindProcess(record.PID)
	if err != nil {
		return nil
	}
	defer proc.Release()
	// Acquire the process handle before checking /proc. On Go/Linux with pidfd
	// support this also pins the signal target across PID reuse after the check.
	start, err := hintProcessStart(record.PID)
	if err != nil || start != record.Start {
		return nil
	}
	if record.Native {
		err = proc.Signal(syscall.SIGTERM)
	} else {
		err = proc.Kill()
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func trackLegacyHint(cfg envConfig, cmd *exec.Cmd) error {
	return trackLegacyHintForSession(cfg, cmd, nil)
}

func trackLegacyHintForSession(cfg envConfig, cmd *exec.Cmd, session *hintSession) error {
	var err error
	if runtime.GOOS == "linux" {
		err = writeLinuxHintSessionPID(cfg, cmd.Process.Pid, false, session)
	} else {
		err = writeHintPID(cfg, cmd.Process.Pid)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if !session.current() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(expand(cfg.hintPIDPath))
		return nil
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// exec copies stderr concurrently. Bound its size and synchronize snapshots.
type hintDiagnostics struct {
	mu   sync.Mutex
	text []byte
}

func (d *hintDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(p)
	if remaining := 16384 - len(d.text); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		d.text = append(d.text, p...)
	}
	return n, nil
}

func (d *hintDiagnostics) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.TrimSpace(string(d.text))
}

func startLinuxHint(cfg envConfig, helper, text string, timeout time.Duration) error {
	return startLinuxHintForSession(cfg, helper, text, timeout, nil)
}

func startLinuxHintForSession(cfg envConfig, helper, text string, timeout time.Duration, session *hintSession) error {
	if !session.current() {
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	defer w.Close()
	diagnostics := &hintDiagnostics{}
	cmd := exec.Command(helper)
	cmd.Env = append(os.Environ(), "WISP_HINT_TEXT="+text, "WISP_HINT_READY_FD=3")
	cmd.ExtraFiles = []*os.File{w}
	cmd.Stderr = diagnostics
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start native hint %s: %w", helper, err)
	}
	_ = w.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	fail := func(cause error) error {
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("native hint %s: %w; stderr: %s", helper, cause, diagnostics.String())
	}
	if err := r.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return fail(err)
	}
	ready := make([]byte, len("READY\n"))
	if _, err := io.ReadFull(r, ready); err != nil {
		return fail(fmt.Errorf("waiting for mapped readiness (limit %s): %w", timeout, err))
	}
	if string(ready) != "READY\n" {
		return fail(fmt.Errorf("invalid readiness %q", ready))
	}
	select {
	case err := <-done:
		return fmt.Errorf("native hint %s exited during startup: %v; stderr: %s", helper, err, diagnostics.String())
	default:
	}
	if !session.current() {
		_ = cmd.Process.Kill()
		<-done
		return nil
	}
	if err := writeLinuxHintSessionPID(cfg, cmd.Process.Pid, true, session); err != nil {
		return fail(fmt.Errorf("record hint PID: %w", err))
	}
	// Invalidation does not take our lock: catch cancellation during the PID write.
	// After this check, the matching queued close owns cleanup under the same lock.
	if !session.current() {
		_ = cmd.Process.Kill()
		<-done
		_ = os.Remove(expand(cfg.hintPIDPath))
	}
	return nil
}

func doctorHint(cfg envConfig) {
	if runtime.GOOS != "linux" {
		return
	}
	helper, err := findHintHelper(cfg)
	if err != nil {
		fmt.Printf("  native hint: unavailable (%v)\n", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper, "--check")
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("  native hint: unavailable (%s: %v: %s)\n", helper, err, strings.TrimSpace(string(out)))
		return
	}
	fmt.Printf("  native hint: dependencies ok (%s; display not checked)\n", helper)
}
