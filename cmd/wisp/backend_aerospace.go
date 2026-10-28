package main

import (
	"bufio"
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"time"
)

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
		"--override", "initial_window_height=" + kittyHeight(cfg, inv),
		"/bin/zsh", "-lc", inner,
		"wisp", inv.WorkDir, inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout,
	}
	args = append(args, inv.OriginalArgs...)
	cmd := exec.Command("open", args...)
	return cmd.Start()
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
