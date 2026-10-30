package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPaletteAndHintRouting(t *testing.T) {
	cfg := prefixCatalog(t)
	cfg.hintPIDPath = filepath.Join(t.TempDir(), "hint.pid")
	cfg.socketPath = filepath.Join(t.TempDir(), "absent.sock")
	cfg.self = "/must-not-launch-terminal-or-daemon"
	t.Setenv("WISP_HINT_BACKEND", "stdout")
	t.Setenv("WISP_DAEMON_AUTOSTART", "0")
	t.Setenv("WISP_NO_DAEMON", "")
	t.Setenv("WISP_DAEMON_BYPASS", "")
	for _, command := range []string{"palette", "hint"} {
		for _, sub := range []string{"", "show", "text", "close"} {
			t.Run(command+"/"+sub, func(t *testing.T) {
				output, err := os.CreateTemp(t.TempDir(), "stdout")
				if err != nil {
					t.Fatal(err)
				}
				defer output.Close()
				stdout := os.Stdout
				os.Stdout = output
				defer func() { os.Stdout = stdout }()
				args := []string{command}
				if sub != "" {
					args = append(args, sub)
				}
				if shouldDelegate(args) {
					t.Fatal("native Palette must not delegate")
				}
				if err := run(cfg, args); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(output.Name())
				if err != nil {
					t.Fatal(err)
				}
				want := hintText(cfg) + "\n"
				if sub == "close" {
					want = ""
				}
				if string(got) != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
		}
	}
	if !shouldDelegate([]string{"menu"}) {
		t.Fatal("terminal menu must retain delegation")
	}
}

func TestPaletteCustomHelperAndAliasClose(t *testing.T) {
	cfg := prefixCatalog(t)
	cfg.hintPIDPath = filepath.Join(t.TempDir(), "hint.pid")
	t.Setenv("WISP_HINT_BACKEND", "")
	// Only a harmless stand-in process is launched, never a native GUI helper.
	t.Setenv("WISP_HINT_COMMAND", "exec /bin/sleep 30")
	defer closeHint(cfg)
	if err := runDirect(cfg, []string{"palette", "show"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.hintPIDPath); err != nil {
		t.Fatalf("custom helper was not tracked: %v", err)
	}
	if err := runDirect(cfg, []string{"hint", "close"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.hintPIDPath); !os.IsNotExist(err) {
		t.Fatalf("alias did not close the Palette: %v", err)
	}
}

func TestMenuMissingFzf(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := paletteAttempt(envConfig{})
	if err == nil || err.Error() != "fzf is required for wisp menu" {
		t.Fatalf("unexpected menu dependency error: %v", err)
	}
}

func TestMenuSelectionUsesOwnCatalog(t *testing.T) {
	for _, id := range []string{"shared", "client-only"} {
		t.Run(id, func(t *testing.T) {
			cfg, dir := execFixture(t)
			for _, catalog := range []string{"A", "B"} {
				recorder := filepath.Join(dir, "record-"+catalog)
				script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + shellQuote(filepath.Join(dir, catalog+".record")) + "\n"
				if err := os.WriteFile(recorder, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				ids := []string{"shared"}
				if catalog == "B" {
					ids = append(ids, "client-only")
				}
				var contents strings.Builder
				for _, actionID := range ids {
					contents.WriteString("[[action]]\nid=" + strconv.Quote(actionID) + "\nkind='exec'\ncommand=[" + strconv.Quote(recorder) + "," + strconv.Quote(actionID) + "]\n")
				}
				if err := os.WriteFile(filepath.Join(dir, catalog+".toml"), []byte(contents.String()), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg.actionsPath = filepath.Join(dir, "B.toml")
			daemonCfg := cfg
			daemonCfg.actionsPath = filepath.Join(dir, "A.toml")
			daemonCfg.logPath = filepath.Join(dir, "daemon.log")
			t.Setenv("WISP_ACTIONS_PATH", daemonCfg.actionsPath)
			t.Setenv("WISP_NO_DAEMON", "")
			t.Setenv("WISP_DAEMON_BYPASS", "")
			t.Setenv("WISP_DAEMON_AUTOSTART", "0")
			t.Setenv("WISP_TEST_MENU_SELECTION", id)
			if err := os.WriteFile(filepath.Join(dir, "fzf"), []byte("#!/bin/sh\nprintf '%s\\tSelected\\texec\\n' \"$WISP_TEST_MENU_SELECTION\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			// A real protocol handler serves catalog A, but no daemon process is started.
			// Use a short socket path to stay below macOS's Unix socket length limit.
			socketDir, err := os.MkdirTemp("", "wisp-menu-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(socketDir)
			cfg.socketPath = filepath.Join(socketDir, "d.sock")
			listener, err := net.Listen("unix", cfg.socketPath)
			if err != nil {
				t.Fatal(err)
			}
			requests := make(chan daemonRequest, 8)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					// Capture requests before passing them to the canonical daemon handler.
					_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
					var req daemonRequest
					if err := json.NewDecoder(conn).Decode(&req); err != nil {
						conn.Close()
						continue
					}
					requests <- req
					server, client := net.Pipe()
					go handleDaemonConn(daemonCfg, server)
					_ = json.NewEncoder(client).Encode(req)
					var resp daemonResponse
					_ = json.NewDecoder(client).Decode(&resp)
					client.Close()
					_ = json.NewEncoder(conn).Encode(resp)
					conn.Close()
				}
			}()
			defer func() {
				listener.Close()
				<-done
				close(requests)
				for req := range requests {
					t.Errorf("Menu selection must not contact daemon catalog A: %+v", req)
				}
			}()
			if err := paletteAttempt(cfg); err != nil {
				t.Fatal(err)
			}
			awaitExecFile(t, filepath.Join(dir, "B.record"), id+"\n")
			awaitExecFile(t, cfg.logPath, "completed")
			for _, forbidden := range []string{"A.record", "forbidden"} {
				if _, err := os.Stat(filepath.Join(dir, forbidden)); !os.IsNotExist(err) {
					t.Fatalf("selected action used daemon catalog or GUI backend (%s): %v", forbidden, err)
				}
			}
		})
	}
}

func TestMenuDelegatesStableRun(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty environment", true: "inherited environment"}[inherited], func(t *testing.T) {
			for _, key := range []string{"WISP_ID", "WISP_TITLE", "WISP_PATH", "WISP_DIR", "WISP_LAYOUT"} {
				value := ""
				if inherited {
					value = "must-not-inherit-" + key
				}
				t.Setenv(key, value)
			}
			t.Setenv("WISP_NO_DAEMON", "")
			t.Setenv("WISP_DAEMON_BYPASS", "")
			t.Setenv("WISP_DAEMON_AUTOSTART", "0")
			t.Setenv("FZF_DEFAULT_OPTS", "")
			t.Setenv("FZF_DEFAULT_OPTS_FILE", "")
			t.Setenv("WISP_ACTIONS_PATH", "must-use-client-config-not-this-env")
			// Keep the Unix socket path below macOS's length limit.
			dir, err := os.MkdirTemp("", "wisp-menu-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cfg := hostilePaletteConfig(dir)
			cfg.socketPath = filepath.Join(dir, "d.sock")
			// Replace the client binary with a context recorder, never the real picker.
			script := "#!/bin/sh\nexec " + shellQuote(binary) + " -test.run=^TestPaletteContextHelper$ -- \"$@\"\n"
			if err := os.WriteFile(cfg.self, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.socketPath, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			requests := make(chan daemonRequest, 2)
			done := make(chan error, 1)
			go func() {
				for i := 0; i < 2; i++ {
					conn, err := listener.Accept()
					if err != nil {
						done <- err
						return
					}
					_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
					var req daemonRequest
					err = json.NewDecoder(conn).Decode(&req)
					if err == nil {
						requests <- req
						err = json.NewEncoder(conn).Encode(daemonResponse{OK: true})
					}
					conn.Close()
					if err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			// The fake daemon only captures requests; it never launches a terminal or action.
			if err := run(cfg, []string{"menu"}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if req := <-requests; req.Op != "ping" {
				t.Fatalf("expected daemon probe, got %+v", req)
			}
			req := <-requests
			want := daemonRequest{Op: "launch", Args: []string{"run", "--id", "palette", "--title", "Wisp Menu", "--path", "", "--dir", "", "--layout", "", "--", "/usr/bin/env", "--"}}
			want.Args = append(want.Args, paletteContextEnv(cfg)...)
			want.Args = append(want.Args, cfg.self, "_palette")
			if !reflect.DeepEqual(req, want) {
				t.Fatalf("unsafe menu delegation: got %+v, want %+v", req, want)
			}
			// The existing parser is unchanged from before the command migration.
			daemonCfg := envConfig{self: "/old/daemon/wisp"}
			got, err := parseInvocation(daemonCfg, req.Args)
			if err != nil {
				t.Fatal(err)
			}
			wrapped := invocation{Mode: "run", ID: "palette", Title: "Wisp Menu", Command: "/usr/bin/env", Args: append([]string{"--"}, paletteContextEnv(cfg)...)}
			wrapped.Args = append(wrapped.Args, cfg.self, "_palette")
			if !reflect.DeepEqual(got, wrapped) {
				t.Fatalf("parsed menu differs from wrapped invocation: got %+v, want %+v", got, wrapped)
			}
			if err := resolveInvocation(daemonCfg, &got); err != nil {
				t.Fatal(err)
			}
			direct := invocation{Mode: "run", ID: "palette", Title: "Wisp Menu", Command: cfg.self, Args: []string{"_palette"}}
			if err := resolveInvocation(daemonCfg, &direct); err != nil {
				t.Fatal(err)
			}
			// Direct menu used to infer _palette as a target, yielding workdir ".".
			// The env wrapper must retain the same effective directory without a target.
			directDir, err := filepath.Abs(direct.WorkDir)
			if err != nil {
				t.Fatal(err)
			}
			if got.TargetPath != "" || got.WorkDir != directDir || got.Mode != "run" || got.SurfaceID != "palette" || got.WindowTitle != direct.WindowTitle || got.Layout != direct.Layout {
				t.Fatalf("wrapped menu changed surface or working directory: got %+v, direct %+v", got, direct)
			}
			if !reflect.DeepEqual(hyprlandChildArgs(daemonCfg, got), got.OriginalArgs) {
				t.Fatal("cross-binary wrapper must pass through the strict context guard unchanged")
			}
			for _, provider := range []string{"lua", "hyprlang"} {
				t.Run(provider, func(t *testing.T) {
					calls := mockHyprctl(t, "configProvider: "+provider)
					if err := launchHyprlandKitty(daemonCfg, got); err != nil {
						t.Fatal(err)
					}
					ipc := calls()
					fragment := shellJoin(got.OriginalArgs)
					if provider == "lua" {
						fragment = strings.TrimSuffix(strings.TrimPrefix(luaQuote(fragment), `"`), `"`)
					}
					if len(ipc) != 2 || !strings.Contains(strings.Join(ipc[1], " "), fragment) || !strings.Contains(strings.Join(ipc[1], " "), "wisp-palette") {
						t.Fatalf("provider lost quoted client wrapper or palette identity: %q", ipc)
					}
					// Execute only that child fragment, with stale compositor context.
					cmd := exec.Command("/bin/sh", "-c", "exec "+shellJoin(got.OriginalArgs))
					cmd.Dir = got.WorkDir
					cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "WISP_TEST_PALETTE_CONTEXT=1", "WISP_TEST_UNLISTED=compositor-only", "GORACE=atexit_sleep_ms=0"}
					wantValues := paletteConfigValues(cfg)
					for key, value := range map[string]string{"WISP_NO_DAEMON": "", "WISP_DAEMON_BYPASS": "", "WISP_DAEMON_AUTOSTART": "0", "FZF_DEFAULT_OPTS": "", "FZF_DEFAULT_OPTS_FILE": ""} {
						wantValues[key] = value
						cmd.Env = append(cmd.Env, key+"=stale-compositor-value")
					}
					for key := range paletteConfigValues(cfg) {
						cmd.Env = append(cmd.Env, key+"=stale-compositor-value")
					}
					wantValues["WISP_TEST_UNLISTED"] = "compositor-only"
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("record delegated context: %v: %s", err, out)
					}
					var recorded struct {
						Values   map[string]string
						Delegate bool
					}
					if err := json.Unmarshal(out, &recorded); err != nil {
						t.Fatalf("decode context: %v: %s", err, out)
					}
					if !reflect.DeepEqual(recorded.Values, wantValues) || !recorded.Delegate {
						t.Fatalf("delegated context mismatch: got %+v, want %#v with delegation enabled", recorded, wantValues)
					}
					if _, err := os.Stat(filepath.Join(dir, "injected")); !os.IsNotExist(err) {
						t.Fatalf("shell evaluated client context as code: %v", err)
					}
				})
			}
		})
	}
}

func TestMigrationHelp(t *testing.T) {
	var out bytes.Buffer
	usage(envConfig{command: "wisp"}, &out)
	for _, want := range []string{"palette [show|close|text]", "Native prefix Palette", "wisp menu", "Wisp Menu", "Compatibility alias for palette"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q: %s", want, out.String())
		}
	}
}
