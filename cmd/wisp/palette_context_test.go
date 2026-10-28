package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This replaces _palette with an environment recorder: no fzf, action,
// compositor, or daemon is ever run by the child, even when options contain
// --filter or an inherited environment requests selection/autostart.
func TestPaletteContextHelper(t *testing.T) {
	if os.Getenv("WISP_TEST_PALETTE_CONTEXT") != "1" {
		return
	}
	cfg := loadEnv()
	values := paletteConfigValues(cfg)
	for _, key := range []string{"WISP_NO_DAEMON", "WISP_DAEMON_BYPASS", "WISP_DAEMON_AUTOSTART", "FZF_DEFAULT_OPTS", "FZF_DEFAULT_OPTS_FILE", "WISP_TEST_UNLISTED"} {
		values[key] = os.Getenv(key)
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Values   map[string]string
		Delegate bool
	}{values, daemonUsable()})
	os.Exit(0)
}

func paletteConfigValues(cfg envConfig) map[string]string {
	return map[string]string{
		"WISP_ACTIONS_PATH":       cfg.actionsPath,
		"WISP_SOCKET_PATH":        cfg.socketPath,
		"WISP_LOG_PATH":           cfg.logPath,
		"WISP_SELF":               cfg.self,
		"WISP_CONFIG_HOME":        cfg.configHome,
		"WISP_CACHE_HOME":         cfg.cacheHome,
		"WISP_HINT_PID_PATH":      cfg.hintPIDPath,
		"WISP_SHELL":              cfg.shell,
		"WISP_BACKEND":            cfg.backend,
		"WISP_HYPRLAND_WORKSPACE": cfg.hyprWorkspace,
		"WISP_KITTY_WIDTH":        cfg.kittyInitialWidth,
		"WISP_KITTY_HEIGHT":       cfg.kittyInitialHt,
	}
}

func hostilePaletteConfig(dir string) envConfig {
	// These remain inert argument data, including through shell and Lua quoting.
	hostile := " ' \" $(touch " + filepath.Join(dir, "injected") + ") `touch " + filepath.Join(dir, "injected") + "` \\\n雪"
	return envConfig{
		actionsPath:       filepath.Join(dir, "catalog"+hostile),
		socketPath:        filepath.Join(dir, "candidate.sock"+hostile),
		logPath:           filepath.Join(dir, "candidate.log"+hostile),
		self:              filepath.Join(dir, "candidate ' binary"),
		configHome:        filepath.Join(dir, "config"+hostile),
		cacheHome:         filepath.Join(dir, "cache"+hostile),
		hintPIDPath:       filepath.Join(dir, "hint.pid"+hostile),
		shell:             "/bin/sh",
		backend:           "hyprland",
		hyprWorkspace:     "candidate" + hostile,
		kittyInitialWidth: "70c",
		kittyInitialHt:    "38c",
	}
}

func TestPaletteContextSurvivesCompositorEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, noDaemon, bypass, autostart, fzf, fzfFile string
	}{
		{name: "direct", noDaemon: "1", autostart: "0"},
		{name: "bypass", bypass: "candidate-only", autostart: "0"},
		{name: "delegate clears stale compositor flags"},
		{name: "explicit UI options", noDaemon: "1", fzf: `--prompt='Choose> ' --filter='test only, never executed'`, fzfFile: "/tmp/options ' \" $()\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := hostilePaletteConfig(dir)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nexec " + shellQuote(binary) + " -test.run=^TestPaletteContextHelper$ -- \"$@\"\n"
			if err := os.WriteFile(cfg.self, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			callerFlags := map[string]string{
				"WISP_NO_DAEMON": tc.noDaemon, "WISP_DAEMON_BYPASS": tc.bypass, "WISP_DAEMON_AUTOSTART": tc.autostart,
				"FZF_DEFAULT_OPTS": tc.fzf, "FZF_DEFAULT_OPTS_FILE": tc.fzfFile,
			}
			for key, value := range callerFlags {
				t.Setenv(key, value)
			}
			t.Setenv("WISP_ACTIONS_PATH", "must-use-resolved-config-not-this-env")
			t.Setenv("WISP_TEST_UNLISTED", "caller variable must not be copied")
			inv := invocation{OriginalArgs: []string{cfg.self, "_palette"}}
			args := hyprlandChildArgs(cfg, inv)
			cmd := exec.Command("/bin/sh", "-c", "exec "+shellJoin(args))
			// Deliberately do not inherit the caller's environment. This models
			// the compositor holding old installed paths and delegation flags.
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "WISP_TEST_PALETTE_CONTEXT=1", "WISP_TEST_UNLISTED=compositor-only", "GORACE=atexit_sleep_ms=0"}
			for key := range paletteConfigValues(cfg) {
				cmd.Env = append(cmd.Env, key+"=stale-compositor-value")
			}
			for key := range callerFlags {
				cmd.Env = append(cmd.Env, key+"=stale-compositor-value")
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("record context: %v: %s", err, out)
			}
			var got struct {
				Values   map[string]string
				Delegate bool
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode: %v: %s", err, out)
			}
			want := paletteConfigValues(cfg)
			for key, value := range callerFlags {
				want[key] = value
			}
			want["WISP_TEST_UNLISTED"] = "compositor-only"
			if !reflect.DeepEqual(got.Values, want) {
				t.Fatalf("context mismatch:\ngot: %#v\nwant: %#v", got.Values, want)
			}
			if got.Delegate != (tc.noDaemon == "" && tc.bypass == "") {
				t.Fatalf("wrong delegation policy: %+v", got)
			}
			if _, err := os.Stat(filepath.Join(dir, "injected")); !os.IsNotExist(err) {
				t.Fatalf("shell evaluated path data: %v", err)
			}
		})
	}
}

func TestPaletteContextIsInHyprlandSpawnCommand(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		t.Run(provider, func(t *testing.T) {
			calls := mockHyprctl(t, "configProvider: "+provider)
			cfg := hostilePaletteConfig(t.TempDir())
			t.Setenv("WISP_NO_DAEMON", "1")
			t.Setenv("WISP_DAEMON_BYPASS", "")
			t.Setenv("WISP_DAEMON_AUTOSTART", "0")
			t.Setenv("FZF_DEFAULT_OPTS", "")
			t.Setenv("FZF_DEFAULT_OPTS_FILE", "")
			inv := invocation{SurfaceID: "palette", Layout: "floating", OriginalArgs: []string{cfg.self, "_palette"}}
			if err := launchHyprlandKitty(cfg, inv); err != nil {
				t.Fatal(err)
			}
			got := calls()
			if len(got) != 2 {
				t.Fatalf("unexpected IPC calls: %q", got)
			}
			fragment := shellJoin(hyprlandChildArgs(cfg, inv))
			if provider == "lua" {
				fragment = strings.TrimSuffix(strings.TrimPrefix(luaQuote(fragment), `"`), `"`)
			}
			if !strings.Contains(strings.Join(got[1], " "), fragment) {
				t.Fatalf("spawn omitted quoted context: %q", got[1])
			}
		})
	}
}

func TestOrdinaryHyprlandChildrenDoNotReceivePaletteContext(t *testing.T) {
	cfg := envConfig{self: "/candidate/wisp"}
	for _, args := range [][]string{{"/usr/bin/true"}, {cfg.self, "help"}, {"/installed/wisp", "_palette"}, {cfg.self, "_palette", "extra"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			inv := invocation{SurfaceID: "palette", OriginalArgs: args}
			if got := hyprlandChildArgs(cfg, inv); !reflect.DeepEqual(got, args) {
				t.Fatalf("ordinary launch changed: %q", got)
			}
		})
	}
}
