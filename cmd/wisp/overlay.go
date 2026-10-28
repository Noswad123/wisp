package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// All Wisp processes in one compositor session share this lock, regardless of
// daemon/socket/config overrides. Never unlink a flock file (that splits locks).
func overlaySessionPath(suffix string) string {
	key := sha256.Sum256([]byte(os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")))
	return filepath.Join(runtimeDir(), fmt.Sprintf("wisp-%x%s", key[:12], suffix))
}

// The local token also establishes Go memory synchronization between daemon
// goroutines; flock alone coordinates processes but is invisible to Go's race
// detector/memory model.
var overlayToken = make(chan struct{}, 1)

func withOverlayLock(fn func() error) error {
	deadline := time.Now().Add(15 * time.Second)
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case overlayToken <- struct{}{}:
		defer func() { <-overlayToken }()
	case <-timer.C:
		return errors.New("Wisp overlay busy: timed out acquiring process lock; retry")
	}
	path := overlaySessionPath(".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("Wisp overlay busy: timed out acquiring session lock; retry")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

type hyprWorkspace struct {
	Name string `json:"name"`
}
type hyprClient struct {
	Address      string        `json:"address"`
	Class        string        `json:"class"`
	InitialClass string        `json:"initialClass"`
	Title        string        `json:"title"`
	InitialTitle string        `json:"initialTitle"`
	Workspace    hyprWorkspace `json:"workspace"`
	Mapped       bool          `json:"mapped"`
	Hidden       bool          `json:"hidden"`
	Floating     bool          `json:"floating"`
	Fullscreen   int           `json:"fullscreen"`
	Size         [2]int        `json:"size"`
}
type hyprMonitor struct {
	Name      string        `json:"name"`
	Focused   bool          `json:"focused"`
	Special   hyprWorkspace `json:"specialWorkspace"`
	Width     int           `json:"width"`
	Height    int           `json:"height"`
	Scale     float64       `json:"scale"`
	Transform int           `json:"transform"`
	Reserved  [4]int        `json:"reserved"` // left, top, right, bottom in logical pixels
}

type overlayController struct {
	cfg      envConfig
	lua      bool
	query    func(string, any) error
	dispatch func(bool, string, string, string) error
	spawn    func(invocation) error
	now      func() time.Time
	sleep    func(time.Duration)
	timeout  time.Duration
}

// Lua adapter contract checked against Hyprland v0.56.2 via gh:
// src/config/lua/bindings/{LuaBindingsDispatchers,LuaBindingsInternal}.cpp
// and src/debug/HyprCtl.cpp. In particular float uses action="enable"
// ("set" silently means toggle!), resize uses x/y with relative=false,
// center accepts window, and hyprctl dispatch wraps a dispatcher in hl.dispatch.

func newOverlayController(cfg envConfig) (*overlayController, error) {
	if !hyprlandAvailable() {
		return nil, errors.New("Hyprland backend requires hyprctl and HYPRLAND_INSTANCE_SIGNATURE")
	}
	lua, err := hyprlandLuaProvider()
	if err != nil {
		return nil, err
	}
	if cfg.hyprWorkspace == "" {
		cfg.hyprWorkspace = "wisp"
	}
	return &overlayController{cfg: cfg, lua: lua, query: func(name string, dst any) error {
		out, err := hyprctlOutput("-j", name)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(out, dst); err != nil {
			return fmt.Errorf("decode hyprctl %s (%s): %w", name, out, err)
		}
		return nil
	}, dispatch: hyprlandDispatch, spawn: func(inv invocation) error { return spawnHyprlandKitty(cfg, inv, lua) }, now: time.Now, sleep: time.Sleep, timeout: 6 * time.Second}, nil
}

func (c *overlayController) dsp(name, arg, expression string) error {
	return c.dispatch(c.lua, name, arg, expression)
}
func (c *overlayController) workspace() string { return "special:" + c.cfg.hyprWorkspace }

// HyprCtl reports untransformed pixel dimensions. Mirror Monitor.cpp's logical
// size conversion, then subtract its logical reserved area (e.g. panels).
func (m hyprMonitor) usableSize() ([2]int, error) {
	if m.Width <= 0 || m.Height <= 0 || m.Scale <= 0 || math.IsNaN(m.Scale) || math.IsInf(m.Scale, 0) {
		return [2]int{}, fmt.Errorf("monitor %q has invalid dimensions/scale: %dx%d at %v", m.Name, m.Width, m.Height, m.Scale)
	}
	w, h := m.Width, m.Height
	if m.Transform%2 != 0 {
		w, h = h, w
	}
	usable := [2]int{int(math.Round(float64(w)/m.Scale)) - m.Reserved[0] - m.Reserved[2], int(math.Round(float64(h)/m.Scale)) - m.Reserved[1] - m.Reserved[3]}
	if usable[0] <= 0 || usable[1] <= 0 {
		return [2]int{}, fmt.Errorf("monitor %q has no usable logical area", m.Name)
	}
	return usable, nil
}

func (c *overlayController) initialSize() (preferred, usable [2]int, err error) {
	var monitors []hyprMonitor
	if err := c.query("monitors", &monitors); err != nil {
		return preferred, usable, err
	}
	for _, m := range monitors {
		if m.Special.Name != c.workspace() {
			continue
		}
		usable, err = m.usableSize()
		if err != nil {
			return preferred, usable, err
		}
		return [2]int{min(900, usable[0]), min(600, usable[1])}, usable, nil
	}
	return preferred, usable, errors.New("overlay has no visible monitor for initial geometry")
}

func (c *overlayController) focusAndRaise(address string) error {
	sel := "address:" + address
	q := luaQuote(sel)
	if err := c.dsp("focuswindow", sel, "hl.dsp.focus({window = "+q+"})"); err != nil {
		return err
	}
	// Focus is not a raise for an already-floating window. Do not use the
	// focused-window z-order fallback until the exact target is observed.
	if err := c.wait("exact focus before raising "+sel, func() (bool, error) {
		var active hyprClient
		if err := c.query("activewindow", &active); err != nil {
			return false, err
		}
		return active.Address == address && active.Mapped && !active.Hidden && active.Workspace.Name == c.workspace(), nil
	}); err != nil {
		return err
	}
	// Recheck within the compositor's Lua evaluation as well: another input
	// event can change focus after the IPC snapshot. Query bindings and the
	// window.address property are supported in 0.56.2. The legacy adapter also
	// supplies the address instead of using bringactivetotop's implicit target.
	raise := "(function() local w = hl.get_active_window(); if not w or w.address ~= " + luaQuote(address) + " then error(\"Wisp target lost focus before raise\") end; return hl.dsp.window.alter_zorder({mode = \"top\", window = " + q + "}) end)()"
	return c.dsp("alterzorder", "top,"+sel, raise)
}

func (c *overlayController) restorePalette() error {
	var monitors []hyprMonitor
	if err := c.query("monitors", &monitors); err != nil {
		return err
	}
	visible := false
	for _, m := range monitors {
		visible = visible || m.Special.Name == c.workspace()
	}
	if !visible {
		return nil
	}
	var clients []hyprClient
	if err := c.query("clients", &clients); err != nil {
		return err
	}
	for _, w := range clients {
		if wispMatch(w, "palette") && w.Mapped && !w.Hidden && w.Address != "" && w.Workspace.Name == c.workspace() {
			return c.focusAndRaise(w.Address)
		}
	}
	return nil
}
func (c *overlayController) wait(label string, observe func() (bool, error)) error {
	deadline := c.now().Add(c.timeout)
	for {
		ok, err := observe()
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if ok {
			return nil
		}
		if !c.now().Before(deadline) {
			return fmt.Errorf("timed out waiting for %s; app left running (not safe to relaunch); inspect hyprctl -j clients / monitors, then retry show or summon", label)
		}
		c.sleep(25 * time.Millisecond)
	}
}

// A toggle is used only after observing the opposite state under the session
// lock. Successful focus is NOT evidence that a special workspace is visible.
func (c *overlayController) visibility(show bool) error {
	var monitors []hyprMonitor
	if err := c.query("monitors", &monitors); err != nil {
		return err
	}
	var shown, focused string
	for _, m := range monitors {
		if m.Focused {
			focused = m.Name
		}
		if m.Special.Name == c.workspace() {
			shown = m.Name
		}
	}
	if focused == "" {
		return errors.New("Hyprland reports no focused monitor; cannot safely present overlay")
	}
	if !show && shown == "" {
		return nil
	}
	target := focused
	if shown != "" {
		target = shown
	}
	if target != focused {
		if err := c.dsp("focusmonitor", target, "hl.dsp.focus({monitor = "+luaQuote(target)+"})"); err != nil {
			return err
		}
	}
	if (shown != "") != show {
		if err := c.dsp("togglespecialworkspace", c.cfg.hyprWorkspace, "hl.dsp.workspace.toggle_special("+luaQuote(c.cfg.hyprWorkspace)+")"); err != nil {
			return err
		}
	}
	return c.wait(fmt.Sprintf("overlay visible=%v", show), func() (bool, error) {
		var ms []hyprMonitor
		if err := c.query("monitors", &ms); err != nil {
			return false, err
		}
		visible := false
		for _, m := range ms {
			visible = visible || m.Special.Name == c.workspace()
		}
		return visible == show, nil
	})
}

func overlayCommand(cfg envConfig, show bool) error {
	backend, err := selectBackend(cfg)
	if err != nil {
		return err
	}
	if backend != "hyprland" {
		return errors.New("show/hide require the Hyprland overlay backend")
	}
	return withOverlayLock(func() error {
		c, err := newOverlayController(cfg)
		if err != nil {
			return err
		}
		return c.visibility(show)
	})
}

func wispMatch(w hyprClient, id string) bool {
	class := "wisp-" + id
	if w.Class == class || w.InitialClass == class {
		return true
	}
	// Compatibility with older kitty Wisp windows: do not steal arbitrary titles.
	return (w.Class == "kitty" || w.InitialClass == "kitty") &&
		(strings.HasPrefix(w.Title, "wisp:"+id+":") || strings.HasPrefix(w.InitialTitle, "wisp:"+id+":"))
}

func (c *overlayController) launch(inv invocation) error {
	var clients []hyprClient
	if err := c.query("clients", &clients); err != nil {
		return err
	}
	if inv.Mode == "summon" || inv.SurfaceID == "palette" {
		// Prefer stable app class even if the application's title has changed.
		for _, w := range clients {
			if w.Class == "wisp-"+inv.SurfaceID || w.InitialClass == "wisp-"+inv.SurfaceID {
				return c.summon(w, inv)
			}
		}
		for _, w := range clients {
			if wispMatch(w, inv.SurfaceID) {
				return c.summon(w, inv)
			}
		}
	}
	// Serialized baseline snapshots retain stable class identity. Only a new
	// address with our exact class can be this launch. Non-Wisp spawns cannot
	// match; external manual spawns using identical Wisp classes remain ambiguous.
	baseline := map[string]bool{}
	for _, w := range clients {
		baseline[w.Address] = true
	}
	// Persist uncertainty across calls/processes: a timed-out spawn may map late.
	// Never duplicate it just because a query or readiness check failed.
	pending := overlaySessionPath("-" + slugify(inv.SurfaceID) + ".pending")
	if _, err := os.Stat(pending); err == nil {
		return fmt.Errorf("previous launch of %s is uncertain; inspect hyprctl -j clients, summon when mapped, or remove %s only after confirming it exited", inv.SurfaceID, pending)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(pending, []byte(inv.WindowTitle), 0o600); err != nil {
		return err
	}
	if err := c.spawn(inv); err != nil {
		return fmt.Errorf("launch uncertain (guard %s): %w", pending, err)
	}
	var mapped hyprClient
	err := c.wait("mapped Wisp surface "+inv.SurfaceID, func() (bool, error) {
		var ws []hyprClient
		if err := c.query("clients", &ws); err != nil {
			return false, err
		}
		for _, w := range ws {
			if !baseline[w.Address] && w.Address != "" && wispMatch(w, inv.SurfaceID) && w.Mapped {
				mapped = w
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("%w (launch guard: %s)", err, pending)
	}
	if err := c.present(mapped, inv, true); err != nil {
		return err
	}
	return os.Remove(pending)
}

func (c *overlayController) summon(w hyprClient, inv invocation) error {
	path := overlaySessionPath("-" + slugify(inv.SurfaceID) + ".pending")
	_, pendingErr := os.Stat(path)
	if pendingErr != nil && !errors.Is(pendingErr, os.ErrNotExist) {
		return pendingErr
	}
	if err := c.present(w, inv, pendingErr == nil); err != nil {
		return err
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *overlayController) applyLayout(w hyprClient, inv invocation, preferred [2]int, resize bool) error {
	sel := "address:" + w.Address
	q := luaQuote(sel)
	if inv.Layout == "fullscreen" {
		if c.lua {
			if err := c.dsp("", "", "hl.dsp.window.fullscreen({action = \"set\", window = "+q+"})"); err != nil {
				return err
			}
		} else if w.Fullscreen == 0 {
			// Legacy fullscreen is focus-only. Focus the exact surface first.
			if err := c.dsp("focuswindow", sel, ""); err != nil {
				return err
			}
			if err := c.dsp("fullscreen", "0", ""); err != nil {
				return err
			}
		}
	} else {
		if c.lua {
			if err := c.dsp("", "", "hl.dsp.window.fullscreen_state({internal = 0, client = 0, window = "+q+"})"); err != nil {
				return err
			}
		} else if w.Fullscreen != 0 {
			if err := c.dsp("focuswindow", sel, ""); err != nil {
				return err
			}
			if err := c.dsp("fullscreenstate", "0 0", ""); err != nil {
				return err
			}
		}
		if err := c.dsp("setfloating", sel, "hl.dsp.window.float({action = \"enable\", window = "+q+"})"); err != nil {
			return err
		}
		if resize {
			if err := c.dsp("resizewindowpixel", fmt.Sprintf("exact %d %d,%s", preferred[0], preferred[1], sel), fmt.Sprintf("hl.dsp.window.resize({x = %d, y = %d, relative = false, window = %s})", preferred[0], preferred[1], q)); err != nil {
				return err
			}
			// Legacy centerwindow has no address selector; focus exact target first.
			if err := c.dsp("focuswindow", sel, "hl.dsp.focus({window = "+q+"})"); err != nil {
				return err
			}
			if err := c.dsp("centerwindow", "", "hl.dsp.window.center({window = "+q+"})"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *overlayController) present(w hyprClient, inv invocation, initial bool) error {
	if w.Address == "" {
		return errors.New("matching Wisp window has no address; refusing duplicate launch")
	}
	sel := "address:" + w.Address
	q := luaQuote(sel)
	if w.Workspace.Name != c.workspace() {
		if err := c.dsp("movetoworkspacesilent", c.workspace()+","+sel, "hl.dsp.window.move({workspace = "+luaQuote(c.workspace())+", follow = false, window = "+q+"})"); err != nil {
			return err
		}
	}
	if err := c.visibility(true); err != nil {
		return err
	}
	if err := c.wait("mapped "+sel, func() (bool, error) {
		var ws []hyprClient
		if err := c.query("clients", &ws); err != nil {
			return false, err
		}
		for _, v := range ws {
			if v.Address == w.Address {
				w = v
				return v.Mapped && !v.Hidden && v.Workspace.Name == c.workspace(), nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	// A previously tiled/fullscreen surface has no user-chosen floating geometry
	// to preserve. Normalize it once when adopting the floating overlay policy.
	resize := inv.Layout != "fullscreen" && (initial || !w.Floating || w.Fullscreen != 0 || w.Size[0] <= 0 || w.Size[1] <= 0)
	var preferred, usable [2]int
	if resize {
		var err error
		preferred, usable, err = c.initialSize()
		if err != nil {
			return err
		}
		if inv.TerminalRows > 0 {
			// Cell counts cannot be converted to logical pixels here. Remove
			// tile/fullscreen state first, then use this client's normal mapped
			// height, not the generic 600px preference or fullscreen dimensions.
			if err := c.applyLayout(w, inv, preferred, false); err != nil {
				return err
			}
			if err := c.wait("normal terminal geometry "+sel, func() (bool, error) {
				var clients []hyprClient
				if err := c.query("clients", &clients); err != nil {
					return false, err
				}
				for _, client := range clients {
					if client.Address == w.Address && client.Mapped && !client.Hidden && client.Floating && client.Fullscreen == 0 && client.Size[1] > 0 {
						w = client
						preferred[1] = min(client.Size[1], usable[1])
						return true, nil
					}
				}
				return false, nil
			}); err != nil {
				return err
			}
		}
	}
	if err := c.applyLayout(w, inv, preferred, resize); err != nil {
		return err
	}
	if err := c.focusAndRaise(w.Address); err != nil {
		return err
	}
	stable, corrections := 0, 0
	return c.wait("visible, focused "+sel, func() (bool, error) {
		var active hyprClient
		if err := c.query("activewindow", &active); err != nil {
			return false, err
		}
		var ms []hyprMonitor
		if err := c.query("monitors", &ms); err != nil {
			return false, err
		}
		visible := false
		for _, m := range ms {
			visible = visible || m.Special.Name == c.workspace()
		}
		layout := active.Floating && active.Fullscreen == 0
		if inv.Layout == "fullscreen" {
			layout = active.Fullscreen != 0
		}
		// Respect compositor/client constraints instead of insisting on the
		// preferred dimensions. Summons never re-impose size or fit bounds.
		size := inv.Layout == "fullscreen" || active.Size[0] > 0 && active.Size[1] > 0
		if resize {
			size = size && active.Size[0] <= usable[0] && active.Size[1] <= usable[1]
		}
		targetReady := visible && active.Address == w.Address && active.Mapped && !active.Hidden && active.Workspace.Name == c.workspace()
		if !targetReady {
			stable = 0
			return false, nil
		}
		if !layout || !size {
			stable = 0
			if initial {
				// Kitty/client startup requests may arrive after the first mapped
				// snapshot and setter. Correct only observed conflicts during this
				// bounded initial handoff, never via a background enforcement loop
				// or while a different surface has focus. Cap repeated conflicts so
				// user/client changes cannot cause unlimited forcing of state.
				if corrections >= 3 {
					return false, fmt.Errorf("startup state for %s did not settle after %d corrections: floating=%v fullscreen=%d size=%v", sel, corrections, active.Floating, active.Fullscreen, active.Size)
				}
				corrections++
				if err := c.applyLayout(active, inv, preferred, resize); err != nil {
					return false, err
				}
				if err := c.focusAndRaise(w.Address); err != nil {
					return false, err
				}
			}
			return false, nil
		}
		if initial {
			// Two consecutive coherent observations avoid accepting the very
			// first post-set snapshot while queued startup requests still land.
			stable++
			return stable >= 2, nil
		}
		return true, nil
	})
}
