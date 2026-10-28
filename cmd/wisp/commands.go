package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func usage(cfg envConfig, w io.Writer) {
	fmt.Fprintf(w, `Usage:
  %[1]s <command> [args...]
  %[1]s --terminal
  %[1]s run [--id ID] [--title TITLE] [--dir DIR] [--path PATH] [--layout floating|fullscreen] -- <command> [args...]
  %[1]s shell [--id ID] [--title TITLE] [--dir DIR]
  %[1]s summon <id> [--title TITLE] [--dir DIR] [--path PATH] -- <command> [args...]
  %[1]s action <id>
  %[1]s actions [list|init|path]
  %[1]s palette
  %[1]s show|hide
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
  WISP_LAYOUT   Surface layout hint on macOS/fallback. Linux uses --layout or action layout (default floating).
  WISP_SHELL    Shell for --terminal/shell. Defaults to SHELL, then /bin/zsh.
  WISP_BACKEND  Backend override: auto, aerospace, hyprland, kitty.
  WISP_ACTIONS_PATH  Action catalog override. Linux prefers actions.linux.toml when present; otherwise actions.toml in config home.
  WISP_LOG_PATH  Log path for palette actions. Defaults to ~/.cache/wisp/wisp.log.
  WISP_SOCKET_PATH  Unix socket for wispd. Defaults to $XDG_RUNTIME_DIR/wispd.sock or /tmp/wisp-$UID/wispd.sock.
  WISP_NO_DAEMON  Run client commands directly without contacting/autostarting wispd.
`, cfg.command)
}

func doctor(cfg envConfig) error {
	fmt.Println("wisp: doctor")
	doctorHint(cfg)
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
space = 'mode wisp'`)
		for _, a := range actions {
			key := strings.ToLower(strings.TrimSpace(a.Key))
			if key == "" || a.ID == "" || key == "space" {
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
bind = , SPACE, submap, wisp`)
		for _, a := range actions {
			key := strings.ToUpper(strings.TrimSpace(a.Key))
			if key == "" || a.ID == "" || key == "SPACE" {
				continue
			}
			fmt.Printf("bind = , %s, exec, sh -lc 'wisp hint close; wisp action %s'\n", key, a.ID)
			fmt.Printf("bind = , %s, submap, reset\n", key)
		}
		fmt.Println("submap = reset")
	default:
		return fmt.Errorf("unknown bindings backend: %s", backend)
	}
	return nil
}
