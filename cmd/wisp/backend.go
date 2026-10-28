package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

func launchInvocation(cfg envConfig, inv invocation) error {
	if err := resolveInvocation(cfg, &inv); err != nil {
		return err
	}
	if inv.Mode == "exec" {
		return launchExec(cfg, inv)
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
		return withOverlayLock(func() error {
			c, err := newOverlayController(cfg)
			if err != nil {
				return err
			}
			if err := c.launch(inv); err != nil {
				// Recover the live palette before releasing the operation lock.
				// Never reveal here: a subsequent explicit hide must stay hidden.
				if recoveryErr := c.restorePalette(); recoveryErr != nil {
					return fmt.Errorf("%w; palette recovery: %v", err, recoveryErr)
				}
				return err
			}
			return nil
		})
	case "kitty":
		return launchFallbackKitty(inv)
	default:
		return fmt.Errorf("unsupported backend: %s", backend)
	}
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

func launchFallbackKitty(inv invocation) error {
	inner := `export WISP=1 WISP_ID="$1" WISP_ROLE="$2" WISP_TITLE="$3" WISP_LAYOUT="$4"; shift 4; exec "$@"`
	args := []string{"--title", inv.WindowTitle, "--working-directory", inv.WorkDir, "/bin/sh", "-lc", inner, "sh", inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout}
	if inv.TerminalRows > 0 {
		args = append([]string{"--start-as", "normal", "-o", "remember_window_size=no", "-o", "initial_window_height=" + strconv.Itoa(inv.TerminalRows) + "c"}, args...)
	}
	args = append(args, inv.OriginalArgs...)
	cmd := exec.Command("kitty", args...)
	return cmd.Start()
}

func kittyHeight(cfg envConfig, inv invocation) string {
	if inv.TerminalRows > 0 {
		return strconv.Itoa(inv.TerminalRows) + "c"
	}
	return cfg.kittyInitialHt
}

func hasKitty() bool {
	if _, err := os.Stat("/Applications/kitty.app"); err == nil {
		return true
	}
	_, err := exec.LookPath("kitty")
	return err == nil
}
