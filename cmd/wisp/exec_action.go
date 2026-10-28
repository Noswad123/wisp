package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type execRequest struct {
	Command string
	Args    []string
	Dir     string
	ID      string
	Title   string
}

type execStarted struct {
	PID   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
}

// A goroutine alone cannot supervise a command after a direct CLI caller exits.
// Re-exec a small detached supervisor instead: it owns/reaps the command and
// logs its exit status even after the palette/CLI has gone away. In a daemon,
// the goroutine below also reaps the supervisor itself. Neither process retains
// the caller's terminal or output pipes. This uses the same Unix APIs on macOS
// and Linux; no backend/compositor involvement is required.
func launchExec(cfg envConfig, inv invocation) error {
	command, err := findExecutable(inv.Command)
	if err != nil {
		return fmt.Errorf("resolve exec action %q: %w", inv.ID, err)
	}
	// Resolve before changing directories, including explicit ./command paths.
	command, err = filepath.Abs(command)
	if err != nil {
		return fmt.Errorf("resolve exec action %q path: %w", inv.ID, err)
	}
	if inv.ID == "" {
		inv.ID = slugify(inv.Command)
	}
	if inv.Title == "" {
		inv.Title = inv.ID
	}
	request, err := json.Marshal(execRequest{Command: command, Args: inv.Args, Dir: inv.WorkDir, ID: inv.ID, Title: inv.Title})
	if err != nil {
		return err
	}
	logPath := expand(cfg.logPath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create exec log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open exec log %q: %w", logPath, err)
	}
	defer logFile.Close()
	status, report, err := os.Pipe()
	if err != nil {
		return err
	}
	defer status.Close()
	defer report.Close()
	if err := status.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set exec startup deadline: %w", err)
	}
	supervisor := exec.Command(cfg.self, "_exec_supervise", string(request))
	supervisor.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	supervisor.Stdout = logFile
	supervisor.Stderr = logFile
	supervisor.ExtraFiles = []*os.File{report} // fd 3: startup acknowledgement only
	if err := supervisor.Start(); err != nil {
		return fmt.Errorf("start exec supervisor for %q: %w", inv.ID, err)
	}
	_ = report.Close()
	go func() { _ = supervisor.Wait() }()
	var started execStarted
	if err := json.NewDecoder(status).Decode(&started); err != nil {
		// A lost acknowledgement does not prove the app failed to start. Do not
		// kill a possibly running GUI (nor silently retry its launch).
		return fmt.Errorf("exec action %q startup outcome uncertain: %w; inspect %s before retrying", inv.ID, err, logPath)
	}
	if started.Error != "" {
		return fmt.Errorf("start exec action %q: %s (log: %s)", inv.ID, started.Error, logPath)
	}
	if started.PID <= 0 {
		return fmt.Errorf("exec action %q returned no started PID; inspect %s before retrying", inv.ID, logPath)
	}
	return nil
}

func superviseExec(args []string) error {
	report := os.NewFile(3, "exec-startup")
	if report == nil {
		return errors.New("missing startup descriptor")
	}
	defer report.Close()
	syscall.CloseOnExec(int(report.Fd()))
	ack := func(status execStarted) {
		// If the client went away, still supervise/reap the app and log its
		// eventual outcome. Only this supervisor holds the acknowledgement fd.
		_ = json.NewEncoder(report).Encode(status)
		_ = report.Close()
	}
	var req execRequest
	if len(args) != 1 {
		err := errors.New("expected one exec request")
		ack(execStarted{Error: err.Error()})
		return err
	}
	if err := json.Unmarshal([]byte(args[0]), &req); err != nil {
		ack(execStarted{Error: err.Error()})
		return err
	}
	cmd := exec.Command(req.Command, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = append(os.Environ(),
		"WISP=1",
		"WISP_ID="+req.ID,
		"WISP_ROLE=exec",
		"WISP_TITLE="+req.Title,
	)
	// nil stdin is /dev/null, not the palette's terminal. Files rather than
	// pipes allow unlimited output without blocking on a vanished reader.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		ack(execStarted{Error: err.Error()})
		return fmt.Errorf("start action=%q command=%q: %w", req.ID, req.Command, err)
	}
	fmt.Fprintf(os.Stderr, "[%s] exec action=%q pid=%d started\n", time.Now().Format(time.RFC3339), req.ID, cmd.Process.Pid)
	ack(execStarted{PID: cmd.Process.Pid})
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("[%s] exec action=%q pid=%d failed: %w", time.Now().Format(time.RFC3339), req.ID, cmd.Process.Pid, err)
	}
	fmt.Fprintf(os.Stderr, "[%s] exec action=%q pid=%d completed\n", time.Now().Format(time.RFC3339), req.ID, cmd.Process.Pid)
	return nil
}
