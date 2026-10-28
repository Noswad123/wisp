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
	"strings"
	"time"
)

type daemonRequest struct {
	Op     string   `json:"op"`
	Args   []string `json:"args,omitempty"`
	Action string   `json:"action,omitempty"`
}

type daemonResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func daemonUsable() bool {
	return os.Getenv("WISP_DAEMON_BYPASS") == "" && os.Getenv("WISP_NO_DAEMON") == ""
}

func daemonCommand(cfg envConfig, args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "start":
		if err := startDaemon(cfg); err != nil {
			return err
		}
		fmt.Printf("wispd started at %s\n", cfg.socketPath)
		return nil
	case "serve":
		return serveDaemon(cfg)
	case "status":
		if err := sendDaemon(cfg, daemonRequest{Op: "ping"}); err != nil {
			return fmt.Errorf("wispd is not running at %s", cfg.socketPath)
		}
		fmt.Printf("wispd is running at %s\n", cfg.socketPath)
		return nil
	case "stop":
		return sendDaemon(cfg, daemonRequest{Op: "stop"})
	case "reload":
		return sendDaemon(cfg, daemonRequest{Op: "reload"})
	default:
		return fmt.Errorf("unknown daemon command: %s", sub)
	}
}

func ensureDaemon(cfg envConfig) error {
	if err := pingDaemon(cfg); err == nil {
		return nil
	}
	if os.Getenv("WISP_DAEMON_AUTOSTART") == "0" {
		return errors.New("wispd is not running")
	}
	if err := startDaemon(cfg); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := pingDaemon(cfg); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("timed out waiting for wispd")
}

func startDaemon(cfg envConfig) error {
	if err := pingDaemon(cfg); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(expand(cfg.socketPath)), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(expand(cfg.logPath)), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(expand(cfg.logPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	args := []string{"daemon", "serve"}
	if filepath.Base(cfg.self) == "wispd" {
		args = []string{"serve"}
	}
	cmd := exec.Command(cfg.self, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return nil
}

func serveDaemon(cfg envConfig) error {
	if err := os.MkdirAll(filepath.Dir(expand(cfg.socketPath)), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(expand(cfg.socketPath)); err == nil {
		if pingDaemon(cfg) == nil {
			return fmt.Errorf("wispd is already running at %s", cfg.socketPath)
		}
		_ = os.Remove(expand(cfg.socketPath))
	}
	listener, err := net.Listen("unix", expand(cfg.socketPath))
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(expand(cfg.socketPath))
	logLine(cfg, "wispd listening socket=%s", cfg.socketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go handleDaemonConn(cfg, conn)
	}
}

func handleDaemonConn(cfg envConfig, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	var req daemonRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeDaemonResponse(conn, daemonResponse{OK: false, Message: err.Error()})
		return
	}
	logLine(cfg, "wispd request op=%s args=%s action=%s", req.Op, strings.Join(req.Args, " "), req.Action)
	var err error
	message := "ok"
	switch req.Op {
	case "ping":
		message = "pong"
	case "reload":
		message = "reloaded"
	case "stop":
		message = "stopping"
		go func() {
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		}()
	case "action":
		err = launchActionDirect(cfg, req.Action)
	case "show", "hide":
		err = overlayCommand(cfg, req.Op == "show")
	case "launch":
		err = runDirect(cfg, req.Args)
	default:
		err = fmt.Errorf("unknown daemon operation: %s", req.Op)
	}
	if err != nil {
		writeDaemonResponse(conn, daemonResponse{OK: false, Message: err.Error()})
		return
	}
	writeDaemonResponse(conn, daemonResponse{OK: true, Message: message})
}

func writeDaemonResponse(w io.Writer, resp daemonResponse) {
	_ = json.NewEncoder(w).Encode(resp)
}

func pingDaemon(cfg envConfig) error {
	return sendDaemon(cfg, daemonRequest{Op: "ping"})
}

func sendDaemon(cfg envConfig, req daemonRequest) error {
	conn, err := net.DialTimeout("unix", expand(cfg.socketPath), 300*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	timeout := 90 * time.Second
	if req.Op == "ping" {
		timeout = 2 * time.Second
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	var resp daemonResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Message)
	}
	return nil
}
