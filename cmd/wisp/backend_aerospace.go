package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func launchMacKitty(cfg envConfig, inv invocation) error {
	if !hasKitty() {
		return errors.New("kitty was not found")
	}
	inner := `work_dir="$1"; wisp_id="$2"; wisp_role="$3"; wisp_title="$4"; wisp_layout="$5"; shift 5; cd -- "$work_dir" || exit; export WISP=1 WISP_ID="$wisp_id" WISP_ROLE="$wisp_role" WISP_TITLE="$wisp_title" WISP_LAYOUT="$wisp_layout"; exec "$@"`
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "open", args...)
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

func postLaunchAerospaceLayout(ctx context.Context, inv invocation) {
	if _, err := exec.LookPath("aerospace"); err != nil {
		return
	}
	correctAerospaceLayout(ctx, inv, func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "aerospace", args...)
		// Bound pipe draining too if a misbehaving command leaves descendants behind.
		cmd.WaitDelay = 10 * time.Millisecond
		return cmd.Output()
	})
}

func correctAerospaceLayout(ctx context.Context, inv invocation, run func(context.Context, ...string) ([]byte, error)) {
	// Focus is not launch identity: the original terminal may still be focused
	// while Kitty registers the new surface, or focus may change after lookup.
	for ctx.Err() == nil {
		out, err := run(ctx, "list-windows", "--all", "--format", "%{window-id}|%{app-bundle-id}|%{window-title}")
		if err != nil {
			return
		}
		id := ""
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			fields := strings.SplitN(scanner.Text(), "|", 3)
			if len(fields) != 3 {
				return
			}
			n, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil || n == 0 || strings.Trim(fields[0], "0123456789") != "" {
				return
			}
			if fields[1] != "net.kovidgoyal.kitty" || fields[2] != inv.WindowTitle {
				continue
			}
			if id != "" {
				return
			}
			id = fields[0]
		}
		if scanner.Err() != nil {
			return
		}
		if id != "" {
			args := []string{"layout", "floating", "--window-id", id}
			if inv.Layout == "fullscreen" {
				args = []string{"fullscreen", "on", "--window-id", id}
			}
			if ctx.Err() == nil {
				_, _ = run(ctx, args...)
			}
			return
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
