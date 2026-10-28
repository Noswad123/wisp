package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func launchHyprlandKitty(cfg envConfig, inv invocation) error {
	if !hyprlandAvailable() {
		return errors.New("Hyprland backend requested, but hyprctl/HYPRLAND_INSTANCE_SIGNATURE is not available")
	}
	lua, err := hyprlandLuaProvider()
	if err != nil {
		return err
	}
	return spawnHyprlandKitty(cfg, inv, lua)
}

func spawnHyprlandKitty(cfg envConfig, inv invocation, lua bool) error {
	inner := `work_dir="$1"; wisp_id="$2"; wisp_role="$3"; wisp_title="$4"; wisp_layout="$5"; shift 5; cd -- "$work_dir" && export WISP=1 WISP_ID="$wisp_id" WISP_ROLE="$wisp_role" WISP_TITLE="$wisp_title" WISP_LAYOUT="$wisp_layout" && exec "$@"`
	width, height := cfg.kittyInitialWidth, kittyHeight(cfg, inv)
	if width == "" {
		width = "70c"
	}
	if height == "" {
		height = "38c"
	}
	// Kitty remembers maximize state as well as size. Disable restoration so a
	// post-map client request cannot resurrect a different window's maximization.
	// Explicit fullscreen actions remain controlled by the compositor rules.
	parts := []string{"kitty", "--class", "wisp-" + inv.SurfaceID, "--title", inv.WindowTitle,
		"--start-as", "normal", "-o", "remember_window_size=no",
		"-o", "initial_window_width=" + width, "-o", "initial_window_height=" + height,
		"/bin/sh", "-lc", inner, "sh", inv.WorkDir, inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout}
	parts = append(parts, hyprlandChildArgs(cfg, inv)...)
	// Window.cpp (v0.56.2) parses the trailing "silent" in the workspace
	// effect for both providers. A late map must never reopen an explicitly
	// hidden overlay; only the controller may reveal it after observing a map.
	rules := fmt.Sprintf("[workspace special:%s silent; float; size 900 600; center]", cfg.hyprWorkspace)
	if inv.TerminalRows > 0 {
		rules = fmt.Sprintf("[workspace special:%s silent; float; center]", cfg.hyprWorkspace)
	}
	if inv.Layout == "fullscreen" {
		rules = fmt.Sprintf("[workspace special:%s silent; fullscreen]", cfg.hyprWorkspace)
	}
	luaRules := "{workspace = " + luaQuote("special:"+cfg.hyprWorkspace+" silent")
	if inv.Layout == "fullscreen" {
		luaRules += ", fullscreen = true}"
	} else {
		luaRules += ", float = true"
		if inv.TerminalRows == 0 {
			luaRules += ", size = {900, 600}"
		}
		luaRules += ", center = true}"
	}
	return hyprlandDispatch(lua, "exec", rules+" "+shellJoin(parts), "hl.dsp.exec_cmd("+luaQuote(shellJoin(parts))+", "+luaRules+")")
}

// Detection is read-only, never a trial dispatch. Some versions ignore -j for
// systeminfo and return text; older legacy versions have no provider field.
func hyprlandLuaProvider() (bool, error) {
	out, jsonErr := hyprctlOutput("-j", "systeminfo")
	if jsonErr == nil {
		if provider := hyprlandProvider(out); provider != "" {
			return isLuaProvider(provider)
		}
	}
	out, err := hyprctlOutput("systeminfo")
	if err != nil {
		if jsonErr != nil {
			return false, fmt.Errorf("detect Hyprland config provider: %v; %w", jsonErr, err)
		}
		return false, fmt.Errorf("detect Hyprland config provider: %w", err)
	}
	provider := hyprlandProvider(out)
	if provider == "" {
		return false, nil // Pre-provider Hyprland uses legacy dispatch syntax.
	}
	return isLuaProvider(provider)
}

func hyprlandProvider(out []byte) string {
	var info struct {
		Provider string `json:"configProvider"`
		State    struct {
			Provider string `json:"configProvider"`
		} `json:"state"`
	}
	if json.Unmarshal(out, &info) == nil {
		if info.Provider != "" {
			return info.Provider
		}
		return info.State.Provider
	}
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "configProvider:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isLuaProvider(provider string) (bool, error) {
	switch strings.ToLower(provider) {
	case "lua":
		return true, nil
	case "legacy", "hyprlang":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported Hyprland config provider: %q", provider)
	}
}

func hyprctlOutput(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "hyprctl", args...)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("hyprctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func hyprlandDispatch(lua bool, name, arg, expression string) error {
	args := []string{"dispatch", name, arg}
	if lua {
		args = []string{"dispatch", expression}
	}
	out, err := hyprctlOutput(args...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("hyprctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// Lua strings are not shell strings (nor Go/JSON strings). Fixed-width decimal
// escapes safely encode control bytes without consuming following digits.
func luaQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 32 || c == 127:
			fmt.Fprintf(&b, "\\%03d", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func focusExistingHyprland(inv invocation) (bool, error) {
	if inv.Mode != "summon" || !hyprlandAvailable() {
		return false, nil
	}
	var found bool
	err := withOverlayLock(func() error {
		c, err := newOverlayController(loadEnv())
		if err != nil {
			return err
		}
		var clients []hyprClient
		if err := c.query("clients", &clients); err != nil {
			return err
		}
		for _, w := range clients {
			if wispMatch(w, inv.SurfaceID) {
				found = true
				return c.present(w, inv, false)
			}
		}
		return nil
	})
	return found && err == nil, err
}

func hyprlandAvailable() bool {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		return false
	}
	_, err := exec.LookPath("hyprctl")
	return err == nil
}
