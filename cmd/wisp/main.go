package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type action struct {
	ID      string   `toml:"id"`
	Title   string   `toml:"title"`
	Kind    string   `toml:"kind"`
	Key     string   `toml:"key"`
	Layout  string   `toml:"layout"`
	Command []string `toml:"command"`
}

type daemonRequest struct {
	Op     string   `json:"op"`
	Args   []string `json:"args,omitempty"`
	Action string   `json:"action,omitempty"`
}

type daemonResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type actionsFile struct {
	Actions []action `toml:"action"`
}

type invocation struct {
	Mode         string
	TerminalMode bool
	ID           string
	Title        string
	TargetPath   string
	WorkDir      string
	Command      string
	Args         []string
	OriginalArgs []string
	WindowTitle  string
	SurfaceID    string
	Layout       string
}

type envConfig struct {
	command           string
	shell             string
	backend           string
	hyprWorkspace     string
	configHome        string
	actionsPath       string
	cacheHome         string
	logPath           string
	socketPath        string
	hintPIDPath       string
	self              string
	kittyInitialWidth string
	kittyInitialHt    string
}

func main() {
	cfg := loadEnv()
	if cfg.command == "wispd" {
		if err := daemonCommand(cfg, os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", cfg.command, err)
			os.Exit(1)
		}
		return
	}
	if err := run(cfg, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cfg.command, err)
		os.Exit(1)
	}
}

func loadEnv() envConfig {
	home := homeDir()
	configHome := envDefault("WISP_CONFIG_HOME", filepath.Join(envDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "wisp"))
	cacheHome := envDefault("WISP_CACHE_HOME", filepath.Join(envDefault("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "wisp"))
	self, err := os.Executable()
	if err != nil || self == "" {
		self = os.Args[0]
	}

	return envConfig{
		command:           filepath.Base(os.Args[0]),
		shell:             envDefault("WISP_SHELL", envDefault("SHELL", "/bin/zsh")),
		backend:           envDefault("WISP_BACKEND", "auto"),
		hyprWorkspace:     envDefault("WISP_HYPRLAND_WORKSPACE", "wisp"),
		configHome:        configHome,
		actionsPath:       envDefault("WISP_ACTIONS_PATH", filepath.Join(configHome, "actions.toml")),
		cacheHome:         cacheHome,
		logPath:           envDefault("WISP_LOG_PATH", filepath.Join(cacheHome, "wisp.log")),
		socketPath:        envDefault("WISP_SOCKET_PATH", filepath.Join(runtimeDir(), "wispd.sock")),
		hintPIDPath:       envDefault("WISP_HINT_PID_PATH", filepath.Join(runtimeDir(), "wisp-hint.pid")),
		self:              envDefault("WISP_SELF", self),
		kittyInitialWidth: envDefault("WISP_KITTY_WIDTH", "70c"),
		kittyInitialHt:    envDefault("WISP_KITTY_HEIGHT", "38c"),
	}
}

func run(cfg envConfig, args []string) error {
	if len(args) == 0 {
		usage(cfg, os.Stderr)
		return errors.New("missing command")
	}

	if shouldDelegate(args) && daemonUsable() {
		if err := ensureDaemon(cfg); err == nil {
			return sendDaemon(cfg, daemonRequest{Op: "launch", Args: args})
		}
	}

	return runDirect(cfg, args)
}

func runDirect(cfg envConfig, args []string) error {
	if len(args) == 0 {
		usage(cfg, os.Stderr)
		return errors.New("missing command")
	}

	switch args[0] {
	case "-h", "--help", "help":
		usage(cfg, os.Stdout)
		return nil
	case "daemon":
		return daemonCommand(cfg, args[1:])
	case "doctor":
		return doctor(cfg)
	case "rules":
		backend := "aerospace"
		if len(args) > 1 {
			backend = args[1]
		}
		return rules(backend)
	case "bindings":
		backend := "aerospace"
		if len(args) > 1 {
			backend = args[1]
		}
		return bindings(cfg, backend)
	case "hint":
		return hintCommand(cfg, args[1:])
	case "actions":
		return actionsCommand(cfg, args[1:])
	case "_palette":
		return paletteBody(cfg)
	case "palette":
		inv := invocation{Mode: "run", ID: "palette", Title: "Wisp Palette", Command: cfg.self, Args: []string{"_palette"}}
		return launchInvocation(cfg, inv)
	case "action":
		if len(args) < 2 {
			return errors.New("action requires an id")
		}
		return launchActionDirect(cfg, args[1])
	case "_launch_action":
		if len(args) < 2 {
			return errors.New("_launch_action requires an id")
		}
		time.Sleep(200 * time.Millisecond)
		logLine(cfg, "palette launching action=%s", args[1])
		inv, err := invocationFromAction(cfg, args[1])
		if err != nil {
			return err
		}
		return launchInvocation(cfg, inv)
	}

	inv, err := parseInvocation(cfg, args)
	if err != nil {
		return err
	}
	return launchInvocation(cfg, inv)
}

func shouldDelegate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "action", "summon", "shell", "run", "palette", "-t", "--terminal":
		return true
	case "daemon", "doctor", "rules", "bindings", "hint", "actions", "_palette", "_launch_action", "-h", "--help", "help":
		return false
	default:
		return true
	}
}

func daemonUsable() bool {
	return os.Getenv("WISP_DAEMON_BYPASS") == "" && os.Getenv("WISP_NO_DAEMON") == ""
}

func launchActionDirect(cfg envConfig, id string) error {
	inv, err := invocationFromAction(cfg, id)
	if err != nil {
		return err
	}
	return launchInvocation(cfg, inv)
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

func usage(cfg envConfig, w io.Writer) {
	fmt.Fprintf(w, `Usage:
  %[1]s <command> [args...]
  %[1]s --terminal
  %[1]s run [--id ID] [--title TITLE] [--dir DIR] [--path PATH] -- <command> [args...]
  %[1]s shell [--id ID] [--title TITLE] [--dir DIR]
  %[1]s summon <id> [--title TITLE] [--dir DIR] [--path PATH] -- <command> [args...]
  %[1]s action <id>
  %[1]s actions [list|init|path]
  %[1]s palette
  %[1]s bindings [aerospace|hyprland]
  %[1]s hint [show|close|text]
  %[1]s daemon [start|serve|status|stop|reload]
  %[1]s doctor
  %[1]s rules [aerospace|hyprland]

Environment:
  WISP_ID       Stable surface id. Defaults to command/title-derived id.
  WISP_TITLE    Human title label. Wisp prefixes non-wisp titles as wisp:<id>: <label>.
  WISP_DIR      Working directory override.
  WISP_PATH     Path used to infer title and working directory.
  WISP_LAYOUT   Surface layout hint: floating or fullscreen.
  WISP_SHELL    Shell for --terminal/shell. Defaults to SHELL, then /bin/zsh.
  WISP_BACKEND  Backend override: auto, aerospace, hyprland, kitty.
  WISP_ACTIONS_PATH  Action catalog path. Defaults to ~/.config/wisp/actions.toml.
  WISP_LOG_PATH  Log path for palette actions. Defaults to ~/.cache/wisp/wisp.log.
  WISP_SOCKET_PATH  Unix socket for wispd. Defaults to $XDG_RUNTIME_DIR/wispd.sock or /tmp/wisp-$UID/wispd.sock.
  WISP_NO_DAEMON  Run client commands directly without contacting/autostarting wispd.
`, cfg.command)
}

func parseInvocation(cfg envConfig, args []string) (invocation, error) {
	inv := invocation{
		Mode:       "run",
		ID:         os.Getenv("WISP_ID"),
		Title:      os.Getenv("WISP_TITLE"),
		TargetPath: os.Getenv("WISP_PATH"),
		WorkDir:    os.Getenv("WISP_DIR"),
		Layout:     os.Getenv("WISP_LAYOUT"),
	}

	switch args[0] {
	case "run":
		var rest []string
		rest = parseOptions(&inv, args[1:])
		if len(rest) == 0 {
			return inv, errors.New("missing command")
		}
		inv.Command, inv.Args = rest[0], rest[1:]
	case "shell":
		inv.Mode = "shell"
		inv.TerminalMode = true
		inv.Command = cfg.shell
		rest := parseOptions(&inv, args[1:])
		if len(rest) > 0 {
			return inv, errors.New("shell does not accept command arguments; use run instead")
		}
	case "summon":
		if len(args) < 2 {
			return inv, errors.New("summon requires an id")
		}
		inv.Mode = "summon"
		inv.ID = args[1]
		rest := parseOptions(&inv, args[2:])
		if len(rest) == 0 {
			return inv, errors.New("missing command")
		}
		inv.Command, inv.Args = rest[0], rest[1:]
	case "-t", "--terminal":
		if len(args) > 1 {
			return inv, errors.New("--terminal does not accept arguments")
		}
		inv.Mode = "shell"
		inv.TerminalMode = true
		inv.Command = cfg.shell
	default:
		inv.Command, inv.Args = args[0], args[1:]
	}

	return inv, nil
}

func parseOptions(inv *invocation, args []string) []string {
	for len(args) > 0 {
		switch args[0] {
		case "--":
			return args[1:]
		case "--id":
			if len(args) >= 2 {
				inv.ID = args[1]
				args = args[2:]
				continue
			}
		case "--title":
			if len(args) >= 2 {
				inv.Title = args[1]
				args = args[2:]
				continue
			}
		case "--dir":
			if len(args) >= 2 {
				inv.WorkDir = args[1]
				args = args[2:]
				continue
			}
		case "--path":
			if len(args) >= 2 {
				inv.TargetPath = args[1]
				args = args[2:]
				continue
			}
		}
		return args
	}
	return nil
}

func launchInvocation(cfg envConfig, inv invocation) error {
	if err := resolveInvocation(cfg, &inv); err != nil {
		return err
	}

	backend, err := selectBackend(cfg)
	if err != nil {
		return err
	}

	switch backend {
	case "aerospace":
		if focusExistingAerospace(inv) {
			return nil
		}
		if err := launchMacKitty(cfg, inv); err != nil {
			return err
		}
		if inv.Layout != "fullscreen" {
			go postLaunchAerospaceFloat(inv.WindowTitle)
		}
		return nil
	case "hyprland":
		if focusExistingHyprland(inv) {
			return nil
		}
		return launchHyprlandKitty(cfg, inv)
	case "kitty":
		return launchFallbackKitty(inv)
	default:
		return fmt.Errorf("unsupported backend: %s", backend)
	}
}

func resolveInvocation(cfg envConfig, inv *invocation) error {
	if inv.Command == "" {
		return errors.New("missing command")
	}

	if _, err := findExecutable(inv.Command); err != nil {
		notify(cfg, fmt.Sprintf("%s was not found on PATH", inv.Command))
		return fmt.Errorf("command not found: %s", inv.Command)
	}

	if inv.TerminalMode {
		inv.OriginalArgs = []string{inv.Command}
	} else {
		inv.OriginalArgs = append([]string{inv.Command}, inv.Args...)
	}

	if inv.TargetPath == "" && len(inv.Args) > 0 && !strings.HasPrefix(inv.Args[0], "-") {
		inv.TargetPath = inv.Args[0]
	}

	defaultWorkDir := ""
	displayPath := ""
	if inv.TargetPath != "" {
		target := expand(inv.TargetPath)
		if st, err := os.Stat(target); err == nil && st.IsDir() {
			defaultWorkDir = target
			displayPath = target
		} else {
			defaultWorkDir = filepath.Dir(target)
			displayPath = target
		}
	} else {
		cwd, _ := os.Getwd()
		defaultWorkDir = cwd
	}

	if inv.WorkDir == "" {
		inv.WorkDir = defaultWorkDir
	} else {
		inv.WorkDir = expand(inv.WorkDir)
	}
	if st, err := os.Stat(inv.WorkDir); err != nil || !st.IsDir() {
		return fmt.Errorf("working directory does not exist: %s", inv.WorkDir)
	}

	inv.Layout = strings.ToLower(strings.TrimSpace(inv.Layout))
	if inv.Layout == "" {
		inv.Layout = "floating"
	}
	switch inv.Layout {
	case "floating", "fullscreen":
	default:
		return fmt.Errorf("unsupported layout: %s", inv.Layout)
	}

	label := inv.Command
	if displayPath != "" {
		label = displayPathForUser(displayPath)
	} else if inv.TerminalMode {
		label = "terminal"
	}

	if inv.ID == "" {
		if inv.Title != "" {
			inv.SurfaceID = slugify(inv.Title)
		} else if inv.TerminalMode {
			inv.SurfaceID = "terminal"
		} else {
			inv.SurfaceID = slugify(inv.Command)
		}
	} else {
		inv.SurfaceID = slugify(inv.ID)
	}

	if inv.Title == "" {
		inv.WindowTitle = label
	} else {
		inv.WindowTitle = inv.Title
	}
	if !strings.HasPrefix(inv.WindowTitle, "wisp:") {
		inv.WindowTitle = fmt.Sprintf("wisp:%s: %s", inv.SurfaceID, inv.WindowTitle)
	}
	return nil
}

func actionsCommand(cfg envConfig, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		actions, err := loadActions(cfg.actionsPath)
		if err != nil {
			return err
		}
		for _, a := range actions {
			if a.ID == "" {
				continue
			}
			title := a.Title
			if title == "" {
				title = a.ID
			}
			kind := a.Kind
			if kind == "" {
				kind = "summon"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", a.ID, title, kind, a.Key)
		}
		return nil
	case "init":
		if _, err := os.Stat(expand(cfg.actionsPath)); err == nil {
			return fmt.Errorf("actions file already exists: %s", cfg.actionsPath)
		}
		if err := os.MkdirAll(filepath.Dir(expand(cfg.actionsPath)), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(expand(cfg.actionsPath), []byte(sampleActions()), 0o644); err != nil {
			return err
		}
		fmt.Printf("created %s\n", cfg.actionsPath)
		return nil
	case "path":
		fmt.Println(cfg.actionsPath)
		return nil
	default:
		return fmt.Errorf("unknown actions command: %s", sub)
	}
}

func invocationFromAction(cfg envConfig, id string) (invocation, error) {
	actions, err := loadActions(cfg.actionsPath)
	if err != nil {
		return invocation{}, err
	}
	for _, a := range actions {
		if a.ID != id {
			continue
		}
		kind := a.Kind
		if kind == "" {
			kind = "summon"
		}
		title := a.Title
		if title == "" {
			title = a.ID
		}
		inv := invocation{Mode: kind, ID: a.ID, Title: title, Layout: a.Layout}
		if kind == "shell" {
			inv.TerminalMode = true
			inv.Command = cfg.shell
			return inv, nil
		}
		if len(a.Command) == 0 {
			return inv, fmt.Errorf("action %q has no command", id)
		}
		inv.Command = expand(a.Command[0])
		for _, arg := range a.Command[1:] {
			inv.Args = append(inv.Args, expand(arg))
		}
		return inv, nil
	}
	return invocation{}, fmt.Errorf("action not found: %s", id)
}

func loadActions(path string) ([]action, error) {
	var parsed actionsFile
	if _, err := toml.DecodeFile(expand(path), &parsed); err != nil {
		return nil, err
	}
	return parsed.Actions, nil
}

func paletteBody(cfg envConfig) error {
	if _, err := exec.LookPath("fzf"); err != nil {
		return errors.New("fzf is required for wisp palette")
	}

	actions, err := loadActions(cfg.actionsPath)
	if err != nil {
		return err
	}
	var input strings.Builder
	for _, a := range actions {
		if a.ID == "" {
			continue
		}
		title := a.Title
		if title == "" {
			title = a.ID
		}
		kind := a.Kind
		if kind == "" {
			kind = "summon"
		}
		fmt.Fprintf(&input, "%s\t%s\t%s\n", a.ID, title, kind)
	}

	cmd := exec.Command("fzf", "--prompt=Wisp> ", "--delimiter=\t", "--with-nth=2,3", "--height=100%", "--border", "--ansi")
	cmd.Stdin = strings.NewReader(input.String())
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil
	}
	selection := strings.TrimSpace(out.String())
	if selection == "" {
		return nil
	}
	actionID := strings.SplitN(selection, "\t", 2)[0]
	logLine(cfg, "palette selected action=%s", actionID)
	logLine(cfg, "palette launching action=%s", actionID)
	return run(cfg, []string{"action", actionID})
}

func doctor(cfg envConfig) error {
	fmt.Println("wisp: doctor")
	fmt.Printf("  os: %s\n", runtime.GOOS)
	if hasKitty() {
		fmt.Println("  kitty: ok")
	} else {
		fmt.Println("  kitty: missing")
	}
	if _, err := exec.LookPath("aerospace"); err == nil {
		fmt.Println("  aerospace: ok")
		fmt.Printf("  recommended rule: %s rules aerospace\n", cfg.command)
	} else {
		fmt.Println("  aerospace: not found")
	}
	if hyprlandAvailable() {
		fmt.Println("  hyprland: detected")
	} else {
		fmt.Println("  hyprland: not detected")
	}
	if _, err := os.Stat(expand(cfg.actionsPath)); err == nil {
		fmt.Printf("  actions: %s\n", cfg.actionsPath)
	} else {
		fmt.Printf("  actions: missing (%s)\n", cfg.actionsPath)
	}
	fmt.Printf("  log: %s\n", cfg.logPath)
	fmt.Printf("  socket: %s\n", cfg.socketPath)
	if pingDaemon(cfg) == nil {
		fmt.Println("  daemon: running")
	} else {
		fmt.Println("  daemon: not running")
	}
	if _, err := exec.LookPath("fzf"); err == nil {
		fmt.Println("  fzf: ok")
	} else {
		fmt.Println("  fzf: missing")
	}
	backend, err := selectBackend(cfg)
	if err != nil {
		backend = "unavailable"
	}
	fmt.Printf("  selected backend: %s\n", backend)
	return nil
}

func rules(backend string) error {
	switch backend {
	case "aerospace":
		fmt.Println(`# Add this to your Aerospace config so Wisp windows are classified by identity
# instead of by post-launch focus races.
on-window-detected = [
    { if.app-id = 'net.kovidgoyal.kitty', if.window-title-regex-substring = '^wisp:', run = 'layout floating' },
]`)
	case "hyprland":
		fmt.Println(`# No static Hyprland rule is required. The Hyprland backend launches Wisp with
# one-shot rules equivalent to:
#
# hyprctl dispatch exec '[workspace special:wisp; float; size 900 600; center] kitty --class wisp-<id> --title wisp:<id>:<label> ...'
#
# Override the special workspace name with WISP_HYPRLAND_WORKSPACE.`)
	default:
		return fmt.Errorf("unknown rules backend: %s", backend)
	}
	return nil
}

func hintCommand(cfg envConfig, args []string) error {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "show":
		return hint(cfg)
	case "close":
		return closeHint(cfg)
	case "text":
		fmt.Println(hintText(cfg))
		return nil
	default:
		return fmt.Errorf("unknown hint command: %s", sub)
	}
}

func hint(cfg envConfig) error {
	text := hintText(cfg)
	if err := showHint(cfg, text); err != nil {
		fmt.Println(text)
		return err
	}
	return nil
}

func hintMessage(cfg envConfig) string {
	return strings.ReplaceAll(hintText(cfg), "\n", " · ")
}

func hintText(cfg envConfig) string {
	actions, err := loadActions(cfg.actionsPath)
	if err != nil {
		return "Wisp\nesc  cancel"
	}
	rows := []struct {
		key   string
		label string
	}{}
	for _, a := range actions {
		key := strings.TrimSpace(a.Key)
		if key == "" || a.ID == "" {
			continue
		}
		label := a.Title
		if label == "" {
			label = a.ID
		}
		rows = append(rows, struct {
			key   string
			label string
		}{key: key, label: label})
	}
	rows = append(rows, struct {
		key   string
		label string
	}{key: "esc", label: "cancel"})

	maxKey := 0
	for _, row := range rows {
		if len(row.key) > maxKey {
			maxKey = len(row.key)
		}
	}
	var b strings.Builder
	b.WriteString("Wisp\n")
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%-*s  %s", maxKey, row.key, row.label)
	}
	return b.String()
}

func showHint(cfg envConfig, text string) error {
	_ = closeHint(cfg)
	switch os.Getenv("WISP_HINT_BACKEND") {
	case "stdout":
		fmt.Println(text)
		return nil
	case "notification":
		return notify(cfg, hintMessage(cfg))
	}
	if command := os.Getenv("WISP_HINT_COMMAND"); command != "" {
		cmd := exec.Command("/bin/sh", "-lc", command)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Start(); err != nil {
			return err
		}
		return writeHintPID(cfg, cmd.Process.Pid)
	}
	if runtime.GOOS == "darwin" {
		if helper, err := findHintHelper(cfg); err == nil {
			cmd := exec.Command(helper)
			cmd.Env = append(os.Environ(), "WISP_HINT_TEXT="+text)
			if err := cmd.Start(); err != nil {
				return err
			}
			return writeHintPID(cfg, cmd.Process.Pid)
		}
	}
	if hyprlandAvailable() {
		return exec.Command("hyprctl", "notify", "1", "1800", "rgb(7aa2f7)", hintMessage(cfg)).Run()
	}
	return notify(cfg, hintMessage(cfg))
}

func writeHintPID(cfg envConfig, pid int) error {
	if err := os.MkdirAll(filepath.Dir(expand(cfg.hintPIDPath)), 0o700); err != nil {
		return err
	}
	return os.WriteFile(expand(cfg.hintPIDPath), []byte(strconv.Itoa(pid)), 0o644)
}

func closeHint(cfg envConfig) error {
	data, err := os.ReadFile(expand(cfg.hintPIDPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	_ = os.Remove(expand(cfg.hintPIDPath))
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	_ = proc.Kill()
	return nil
}

func findHintHelper(cfg envConfig) (string, error) {
	if helper := os.Getenv("WISP_HINT_HELPER"); helper != "" {
		return findExecutable(helper)
	}
	selfDir := filepath.Dir(cfg.self)
	for _, candidate := range []string{
		filepath.Join(selfDir, "wisp-hint-macos"),
		"wisp-hint-macos",
	} {
		if path, err := findExecutable(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("wisp-hint-macos was not found")
}

func bindings(cfg envConfig, backend string) error {
	actions, err := loadActions(cfg.actionsPath)
	if err != nil {
		return err
	}
	switch backend {
	case "aerospace":
		fmt.Println(`# Wisp prefix bindings for Aerospace.
# Add this to ~/.aerospace.toml, then reload Aerospace.
[mode.main.binding]
alt-space = ['exec-and-forget wisp hint show', 'mode wisp']

[mode.wisp.binding]
esc = ['exec-and-forget wisp hint close', 'mode main']
space = ['exec-and-forget wisp hint close', 'exec-and-forget wisp palette', 'mode main']`)
		for _, a := range actions {
			key := strings.ToLower(strings.TrimSpace(a.Key))
			if key == "" || a.ID == "" {
				continue
			}
			fmt.Printf("%s = ['exec-and-forget wisp hint close', 'exec-and-forget wisp action %s', 'mode main']\n", key, a.ID)
		}
	case "hyprland":
		fmt.Println(`# Wisp prefix bindings for Hyprland.
# Add this to hyprland.conf, then reload Hyprland.
bind = ALT, SPACE, exec, wisp hint show
bind = ALT, SPACE, submap, wisp

submap = wisp
bind = , ESCAPE, exec, wisp hint close
bind = , ESCAPE, submap, reset
bind = , SPACE, exec, sh -lc 'wisp hint close; wisp palette'`)
		for _, a := range actions {
			key := strings.ToUpper(strings.TrimSpace(a.Key))
			if key == "" || a.ID == "" {
				continue
			}
			fmt.Printf("bind = , %s, exec, sh -lc 'wisp hint close; wisp action %s'\n", key, a.ID)
		}
		fmt.Println("submap = reset")
	default:
		return fmt.Errorf("unknown bindings backend: %s", backend)
	}
	return nil
}

func selectBackend(cfg envConfig) (string, error) {
	switch cfg.backend {
	case "auto":
		if hyprlandAvailable() {
			return "hyprland", nil
		}
		if runtime.GOOS == "darwin" {
			return "aerospace", nil
		}
		return "kitty", nil
	case "aerospace", "hyprland", "kitty":
		return cfg.backend, nil
	default:
		return "", fmt.Errorf("unknown backend: %s", cfg.backend)
	}
}

func launchMacKitty(cfg envConfig, inv invocation) error {
	if !hasKitty() {
		return errors.New("kitty was not found")
	}
	inner := `work_dir="$1"; wisp_id="$2"; wisp_role="$3"; wisp_title="$4"; wisp_layout="$5"; shift 5; cd -- "$work_dir" && export WISP=1 WISP_ID="$wisp_id" WISP_ROLE="$wisp_role" WISP_TITLE="$wisp_title" WISP_LAYOUT="$wisp_layout"; if command -v aerospace >/dev/null 2>&1; then if [ "$wisp_layout" = fullscreen ]; then sleep 0.2; aerospace fullscreen >/dev/null 2>&1 || true; else aerospace layout floating >/dev/null 2>&1 || true; fi; fi; exec "$@"`
	args := []string{"-na", "kitty", "--args",
		"--detach=no", "--single-instance=no",
		"--override", "macos_quit_when_last_window_closed=yes",
		"--title", inv.WindowTitle,
		"--override", "hide_window_decorations=no",
		"--override", "remember_window_size=no",
		"--override", "initial_window_width=" + cfg.kittyInitialWidth,
		"--override", "initial_window_height=" + cfg.kittyInitialHt,
		"/bin/zsh", "-lc", inner,
		"wisp", inv.WorkDir, inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout,
	}
	args = append(args, inv.OriginalArgs...)
	cmd := exec.Command("open", args...)
	return cmd.Start()
}

func launchFallbackKitty(inv invocation) error {
	inner := `export WISP=1 WISP_ID="$1" WISP_ROLE="$2" WISP_TITLE="$3" WISP_LAYOUT="$4"; shift 4; exec "$@"`
	args := []string{"--title", inv.WindowTitle, "--working-directory", inv.WorkDir, "/bin/sh", "-lc", inner, "sh", inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout}
	args = append(args, inv.OriginalArgs...)
	cmd := exec.Command("kitty", args...)
	return cmd.Start()
}

func launchHyprlandKitty(cfg envConfig, inv invocation) error {
	if !hyprlandAvailable() {
		return errors.New("Hyprland backend requested, but hyprctl/HYPRLAND_INSTANCE_SIGNATURE is not available")
	}
	inner := `work_dir="$1"; wisp_id="$2"; wisp_role="$3"; wisp_title="$4"; wisp_layout="$5"; shift 5; cd -- "$work_dir" && export WISP=1 WISP_ID="$wisp_id" WISP_ROLE="$wisp_role" WISP_TITLE="$wisp_title" WISP_LAYOUT="$wisp_layout" && exec "$@"`
	parts := []string{"kitty", "--class", "wisp-" + inv.SurfaceID, "--title", inv.WindowTitle, "/bin/sh", "-lc", inner, "sh", inv.WorkDir, inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout}
	parts = append(parts, inv.OriginalArgs...)
	rules := fmt.Sprintf("[workspace special:%s; float; size 900 600; center]", cfg.hyprWorkspace)
	if inv.Layout == "fullscreen" {
		rules = fmt.Sprintf("[workspace special:%s; fullscreen]", cfg.hyprWorkspace)
	}
	cmd := exec.Command("hyprctl", "dispatch", "exec", rules+" "+shellJoin(parts))
	return cmd.Run()
}

func focusExistingAerospace(inv invocation) bool {
	if inv.Mode != "summon" {
		return false
	}
	if _, err := exec.LookPath("aerospace"); err != nil {
		return false
	}
	cmd := exec.Command("aerospace", "list-windows", "--all", "--format", "%{window-id}|%{app-bundle-id}|%{window-title}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	prefix := "wisp:" + inv.SurfaceID + ":"
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "|", 3)
		if len(fields) != 3 {
			continue
		}
		if fields[1] == "net.kovidgoyal.kitty" && strings.HasPrefix(fields[2], prefix) {
			return exec.Command("aerospace", "focus", "--window-id", fields[0]).Run() == nil
		}
	}
	return false
}

func focusExistingHyprland(inv invocation) bool {
	if inv.Mode != "summon" || !hyprlandAvailable() {
		return false
	}
	out, err := exec.Command("hyprctl", "-j", "clients").Output()
	if err != nil {
		return false
	}
	var clients []struct {
		Address   string `json:"address"`
		Title     string `json:"title"`
		Workspace struct {
			Name string `json:"name"`
		} `json:"workspace"`
	}
	if json.Unmarshal(out, &clients) != nil {
		return false
	}
	prefix := "wisp:" + inv.SurfaceID + ":"
	for _, client := range clients {
		if !strings.HasPrefix(client.Title, prefix) || client.Address == "" {
			continue
		}
		if exec.Command("hyprctl", "dispatch", "focuswindow", "address:"+client.Address).Run() == nil {
			return true
		}
		if strings.HasPrefix(client.Workspace.Name, "special:") {
			_ = exec.Command("hyprctl", "dispatch", "togglespecialworkspace", strings.TrimPrefix(client.Workspace.Name, "special:")).Run()
			return exec.Command("hyprctl", "dispatch", "focuswindow", "address:"+client.Address).Run() == nil
		}
	}
	return false
}

func postLaunchAerospaceFloat(title string) {
	if _, err := exec.LookPath("aerospace"); err != nil {
		return
	}
	for range 30 {
		out, err := exec.Command("aerospace", "list-windows", "--focused", "--format", "%{app-bundle-id}|%{window-title}").Output()
		if err == nil && strings.Contains(string(out), "net.kovidgoyal.kitty|") && strings.Contains(string(out), title) {
			_ = exec.Command("aerospace", "layout", "floating").Run()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func hasKitty() bool {
	if _, err := os.Stat("/Applications/kitty.app"); err == nil {
		return true
	}
	_, err := exec.LookPath("kitty")
	return err == nil
}

func hyprlandAvailable() bool {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		return false
	}
	_, err := exec.LookPath("hyprctl")
	return err == nil
}

func findExecutable(command string) (string, error) {
	if filepath.IsAbs(command) || strings.Contains(command, string(os.PathSeparator)) {
		if st, err := os.Stat(command); err == nil && !st.IsDir() {
			return command, nil
		}
	}
	return exec.LookPath(command)
}

func logLine(cfg envConfig, format string, args ...any) {
	_ = os.MkdirAll(filepath.Dir(expand(cfg.logPath)), 0o755)
	f, err := os.OpenFile(expand(cfg.logPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

func notify(cfg envConfig, message string) error {
	if _, err := exec.LookPath("osascript"); err == nil {
		return exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", message, cfg.command)).Run()
	}
	if _, err := exec.LookPath("notify-send"); err == nil {
		return exec.Command("notify-send", "Wisp mode", message).Run()
	}
	return errors.New("no notification command found")
}

func sampleActions() string {
	return `[[action]]
id = "scratch"
title = "Scratch Notes"
kind = "summon"
key = "s"
command = ["nvim", "~/Projects/darkness/introspection/scratch.md"]

[[action]]
id = "aero"
title = "Aerospace Manager"
kind = "summon"
key = "a"
command = ["zsh", "-lc", "aeros"]

[[action]]
id = "terminal"
title = "Floating Terminal"
kind = "shell"
key = "t"
command = []

[[action]]
id = "waystone"
title = "Waystone"
kind = "summon"
key = "w"
command = ["waystone"]

[[action]]
id = "wyrm"
title = "Wyrm"
kind = "summon"
key = "r"
layout = "fullscreen"
command = ["wyrm"]
`
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func expand(value string) string {
	if strings.HasPrefix(value, "~/") {
		value = filepath.Join(homeDir(), strings.TrimPrefix(value, "~/"))
	}
	return os.ExpandEnv(value)
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return os.Getenv("HOME")
}

func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("wisp-%d", os.Getuid()))
}

func displayPathForUser(path string) string {
	home := homeDir()
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~/" + strings.TrimPrefix(path, home+string(os.PathSeparator))
	}
	return path
}

func slugify(value string) string {
	value = filepath.Base(value)
	value = strings.ToLower(value)
	re := regexp.MustCompile(`[^a-z0-9_.-]+`)
	value = strings.Trim(re.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "surface"
	}
	return value
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
