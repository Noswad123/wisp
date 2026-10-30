package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every hyprctl invocation goes to this subprocess, never the live compositor.
func TestHyprctlHelper(t *testing.T) {
	if os.Getenv("WISP_TEST_HYPRCTL") != "1" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	logPath := os.Getenv("WISP_TEST_LOG")
	previous, _ := os.ReadFile(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	_ = json.NewEncoder(f).Encode(args)
	_ = f.Close()
	key := strings.Join(args, " ")
	switch key {
	case "-j systeminfo":
		fmt.Print(os.Getenv("WISP_TEST_INFO_JSON"))
		if os.Getenv("WISP_TEST_JSON_ERROR") == "1" {
			os.Exit(7)
		}
	case "systeminfo":
		fmt.Print(os.Getenv("WISP_TEST_INFO_TEXT"))
		if os.Getenv("WISP_TEST_TEXT_ERROR") == "1" {
			os.Exit(7)
		}
	case "-j clients":
		if os.Getenv("WISP_TEST_LATE_MAP") != "waiting" && (bytes.Contains(previous, []byte("exec_cmd")) || bytes.Contains(previous, []byte(`"exec"`))) {
			if spawned := os.Getenv("WISP_TEST_SPAWNED"); spawned != "" {
				fmt.Print(spawned)
				break
			}
		}
		fmt.Print(os.Getenv("WISP_TEST_CLIENTS"))
	case "-j monitors":
		shown := ""
		if (bytes.Count(previous, []byte("toggle_special"))+bytes.Count(previous, []byte("togglespecialworkspace")))%2 == 1 {
			shown = "special:wisp"
		}
		// Model Window.cpp's map-time workspace effect: non-silent placement
		// reveals the special workspace independently of the Wisp controller.
		if os.Getenv("WISP_TEST_LATE_MAP") == "mapped" && !bytes.Contains(previous, []byte("special:wisp silent")) {
			shown = "special:wisp"
		}
		fmt.Printf(`[{"name":"DP-1","width":1920,"height":1080,"scale":1,"focused":true,"specialWorkspace":{"name":%q}}]`, shown)
	case "-j activewindow":
		var ws []hyprClient
		data := os.Getenv("WISP_TEST_CLIENTS")
		if spawned := os.Getenv("WISP_TEST_SPAWNED"); spawned != "" {
			data = spawned
		}
		_ = json.Unmarshal([]byte(data), &ws)
		if len(ws) > 0 {
			_ = json.NewEncoder(os.Stdout).Encode(ws[0])
		} else {
			fmt.Print(`{}`)
		}
	default:
		failure := os.Getenv("WISP_TEST_FAILURE")
		focus := strings.Contains(key, "focuswindow") || strings.Contains(key, "hl.dsp.focus(")
		toggle := strings.Contains(key, "togglespecialworkspace") || strings.Contains(key, "toggle_special(")
		firstFocus := focus && !bytes.Contains(previous, []byte("dispatch"))
		if failure == "all" || (failure == "focus" && focus) || (failure == "first-focus" && firstFocus) || (failure == "toggle" && (firstFocus || toggle)) {
			fmt.Print("dispatcher stdout detail\n")
			fmt.Fprint(os.Stderr, "dispatcher stderr detail\n")
			os.Exit(7)
		}
		if response := os.Getenv("WISP_TEST_RESPONSE"); response != "" {
			fmt.Print(response)
			break
		}
		fmt.Print("ok\n")
	}
	os.Exit(0)
}

func mockHyprctl(t *testing.T, info string) func() [][]string {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec " + shellQuote(binary) + " -test.run=^TestHyprctlHelper$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "hyprctl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "calls.jsonl")
	t.Setenv("PATH", dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "mock-only")
	t.Setenv("WISP_TEST_HYPRCTL", "1")
	// Race-instrumented helper subprocesses need no exit grace period: all
	// their work is synchronous, and the default adds a second per IPC query.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	t.Setenv("WISP_TEST_LOG", logPath)
	t.Setenv("WISP_TEST_INFO_JSON", info)
	t.Setenv("WISP_TEST_INFO_TEXT", info)
	t.Setenv("WISP_TEST_JSON_ERROR", "")
	t.Setenv("WISP_TEST_TEXT_ERROR", "")
	t.Setenv("WISP_TEST_FAILURE", "")
	t.Setenv("WISP_TEST_RESPONSE", "")
	t.Setenv("WISP_TEST_CLIENTS", "[]")
	t.Setenv("WISP_TEST_SPAWNED", "")
	t.Setenv("WISP_TEST_LATE_MAP", "")
	return func() [][]string {
		t.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		var calls [][]string
		decoder := json.NewDecoder(bytes.NewReader(data))
		for decoder.More() {
			var args []string
			if err := decoder.Decode(&args); err != nil {
				t.Fatal(err)
			}
			calls = append(calls, args)
		}
		return calls
	}
}

func TestHyprlandProviderDetection(t *testing.T) {
	for _, tc := range []struct {
		name, json, text                 string
		jsonFail, textFail, lua, wantErr bool
		calls                            int
	}{
		{name: "text despite JSON flag", json: "Hyprland 0.56.2\nState:\nconfigProvider: lua\nbackend: drm", lua: true, calls: 1},
		{name: "JSON", json: `{"configProvider":"lua"}`, lua: true, calls: 1},
		{name: "nested JSON", json: `{"state":{"configProvider":"lua"}}`, lua: true, calls: 1},
		{name: "legacy", json: "State:\nconfigProvider: hyprlang", calls: 1},
		{name: "legacy JSON", json: `{"configProvider":"legacy"}`, calls: 1},
		{name: "unsupported JSON", json: "unknown option", jsonFail: true, text: "State:\n  configProvider: lua\n", lua: true, calls: 2},
		{name: "missing JSON field", json: `{}`, text: "configProvider: lua", lua: true, calls: 2},
		{name: "old legacy", json: "old systeminfo", text: "old systeminfo", calls: 2},
		{name: "unknown provider", json: "configProvider: future", wantErr: true, calls: 1},
		{name: "disconnected", json: "JSON socket error", text: "text socket error", jsonFail: true, textFail: true, wantErr: true, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := mockHyprctl(t, tc.json)
			t.Setenv("WISP_TEST_INFO_TEXT", tc.text)
			if tc.jsonFail {
				t.Setenv("WISP_TEST_JSON_ERROR", "1")
			}
			if tc.textFail {
				t.Setenv("WISP_TEST_TEXT_ERROR", "1")
			}
			lua, err := hyprlandLuaProvider()
			if lua != tc.lua || (err != nil) != tc.wantErr {
				t.Fatalf("got lua=%v, err=%v", lua, err)
			}
			got := calls()
			if len(got) != tc.calls {
				t.Fatalf("unexpected calls: %q", got)
			}
			for _, call := range got {
				if call[len(call)-1] != "systeminfo" {
					t.Fatalf("non-read-only detection: %q", call)
				}
			}
			if tc.textFail && (!strings.Contains(err.Error(), tc.json) || !strings.Contains(err.Error(), tc.text)) {
				t.Fatalf("missing hyprctl diagnostics: %v", err)
			}
		})
	}
}

func TestLaunchHyprlandKitty(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		for _, layout := range []string{"floating", "fullscreen"} {
			t.Run(provider+"/"+layout, func(t *testing.T) {
				calls := mockHyprctl(t, "State:\nconfigProvider: "+provider)
				inv := invocation{SurfaceID: "palette", Mode: "run", WorkDir: "/tmp/quoted ' dir", WindowTitle: "wisp:palette:雪 \"'); os.exit(); --\n\\", Layout: layout, OriginalArgs: []string{"/usr/bin/true", "$(touch /never-execute)", "a'b\"c\\d\n7"}}
				cfg := envConfig{hyprWorkspace: "wisp", kittyInitialWidth: "80c", kittyInitialHt: "42c"}
				if provider == "lua" {
					cfg.hyprWorkspace = "wisp\"}); os.exit(); --\n\\"
				}
				if err := launchHyprlandKitty(cfg, inv); err != nil {
					t.Fatal(err)
				}
				inner := `work_dir="$1"; wisp_id="$2"; wisp_role="$3"; wisp_title="$4"; wisp_layout="$5"; shift 5; cd -- "$work_dir" && export WISP=1 WISP_ID="$wisp_id" WISP_ROLE="$wisp_role" WISP_TITLE="$wisp_title" WISP_LAYOUT="$wisp_layout" && exec "$@"`
				parts := []string{"kitty", "--class", "wisp-palette", "--title", inv.WindowTitle,
					"--start-as", "normal", "-o", "remember_window_size=no", "-o", "initial_window_width=80c", "-o", "initial_window_height=42c",
					"/bin/sh", "-lc", inner, "sh", inv.WorkDir, "palette", "run", inv.WindowTitle, layout}
				parts = append(parts, inv.OriginalArgs...)
				command := shellJoin(parts)
				var want []string
				if provider == "lua" {
					rules := "{workspace = " + luaQuote("special:"+cfg.hyprWorkspace+" silent")
					if layout == "fullscreen" {
						rules += ", fullscreen = true}"
					} else {
						rules += ", float = true, size = {900, 600}, center = true}"
					}
					want = []string{"dispatch", "hl.dsp.exec_cmd(" + luaQuote(command) + ", " + rules + ")"}
				} else {
					rules := "[workspace special:wisp silent; float; size 900 600; center]"
					if layout == "fullscreen" {
						rules = "[workspace special:wisp silent; fullscreen]"
					}
					want = []string{"dispatch", "exec", rules + " " + command}
				}
				got := calls()
				if len(got) != 2 || !reflect.DeepEqual(got[1], want) {
					t.Fatalf("calls = %q, want dispatch %q", got, want)
				}
			})
		}
	}
}

func TestSilentSpawnCannotReopenAfterTimeoutAndHide(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		t.Run(provider, func(t *testing.T) {
			mockHyprctl(t, "configProvider: "+provider)
			t.Setenv("WISP_TEST_LATE_MAP", "waiting")
			t.Setenv("WISP_TEST_SPAWNED", `[{"address":"0x123","class":"wisp-wyrm","mapped":true,"floating":true,"size":[800,500],"workspace":{"name":"special:wisp"}}]`)
			c, err := newOverlayController(envConfig{hyprWorkspace: "wisp"})
			if err != nil {
				t.Fatal(err)
			}
			clock := time.Time{}
			c.now = func() time.Time { return clock }
			c.sleep = func(d time.Duration) { clock = clock.Add(d) }
			c.timeout = 50 * time.Millisecond
			if err := c.visibility(true); err != nil {
				t.Fatal(err)
			}
			if err := c.launch(testInvocation()); err == nil {
				t.Fatal("expected map timeout")
			}
			if err := c.visibility(false); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WISP_TEST_LATE_MAP", "mapped")
			var clients []hyprClient
			if err := c.query("clients", &clients); err != nil || len(clients) != 1 {
				t.Fatalf("late map: %v %v", clients, err)
			}
			var monitors []hyprMonitor
			if err := c.query("monitors", &monitors); err != nil {
				t.Fatal(err)
			}
			if monitors[0].Special.Name != "" {
				t.Fatal("non-silent late map reopened explicitly hidden overlay")
			}
			if err := c.visibility(true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFocusExistingHyprland(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		for _, failure := range []string{"", "focus", "toggle"} {
			t.Run(provider+"/"+failure, func(t *testing.T) {
				calls := mockHyprctl(t, "configProvider: "+provider)
				t.Setenv("WISP_TEST_FAILURE", failure)
				address := "0x123\"}); bad(); --\\\n8"
				workspace := "wisp"
				clients, _ := json.Marshal([]map[string]any{{"address": address, "class": "wisp-palette", "mapped": true, "floating": true, "size": []int{900, 600}, "title": "wisp:palette:hostile", "workspace": map[string]string{"name": "special:" + workspace}}})
				t.Setenv("WISP_TEST_CLIENTS", string(clients))
				focused, err := focusExistingHyprland(invocation{Mode: "summon", SurfaceID: "palette"})
				wantErr := failure == "focus" || failure == "toggle"
				if focused == wantErr || (err != nil) != wantErr {
					t.Fatalf("focused=%v err=%v", focused, err)
				}
				if wantErr {
					assertHyprctlError(t, err)
				}
				toggle := []string{"dispatch", "togglespecialworkspace", workspace}
				if provider == "lua" {
					toggle = []string{"dispatch", "hl.dsp.workspace.toggle_special(" + luaQuote(workspace) + ")"}
				}
				got := calls()
				toggles := 0
				for _, call := range got {
					if reflect.DeepEqual(call, toggle) {
						toggles++
					}
				}
				if toggles != 1 {
					t.Fatalf("must reveal hidden workspace even when focus succeeds: %q", got)
				}
				if failure == "" {
					focus := []string{"dispatch", "focuswindow", "address:" + address}
					if provider == "lua" {
						focus = []string{"dispatch", "hl.dsp.focus({window = " + luaQuote("address:"+address) + "})"}
					}
					matched := false
					for _, call := range got {
						matched = matched || reflect.DeepEqual(call, focus)
					}
					if !matched {
						t.Fatalf("missing safely quoted address: %q", got)
					}
				}
			})
		}
	}
}

func assertHyprctlError(t *testing.T, err error) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("lost exit error: %v", err)
	}
	for _, detail := range []string{"hyprctl dispatch", "dispatcher stdout detail", "dispatcher stderr detail"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("missing %q: %v", detail, err)
		}
	}
}

func TestHyprlandLaunchError(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		t.Run(provider, func(t *testing.T) {
			mockHyprctl(t, "configProvider: "+provider)
			t.Setenv("WISP_TEST_FAILURE", "all")
			assertHyprctlError(t, launchHyprlandKitty(envConfig{hyprWorkspace: "wisp"}, invocation{}))
		})
	}
}

func TestHyprlandDispatchErrorWithSuccessfulExit(t *testing.T) {
	mockHyprctl(t, "configProvider: lua")
	t.Setenv("WISP_TEST_RESPONSE", "Invalid dispatcher: compositor detail")
	err := hyprlandDispatch(true, "", "", `hl.dsp.no_op()`)
	if err == nil || !strings.Contains(err.Error(), "hyprctl dispatch") || !strings.Contains(err.Error(), "compositor detail") {
		t.Fatalf("lost successful-exit error: %v", err)
	}
}

func TestDaemonOverlayProtocol(t *testing.T) {
	mockHyprctl(t, "configProvider: lua")
	cfg := envConfig{backend: "hyprland", hyprWorkspace: "wisp", logPath: filepath.Join(t.TempDir(), "daemon.log")}
	for _, req := range []daemonRequest{{Op: "launch", Args: []string{"show"}}, {Op: "hide"}} {
		server, client := net.Pipe()
		done := make(chan struct{})
		go func() { handleDaemonConn(cfg, server); close(done) }()
		if err := json.NewEncoder(client).Encode(req); err != nil {
			t.Fatal(err)
		}
		var resp daemonResponse
		if err := json.NewDecoder(client).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		client.Close()
		<-done
		if !resp.OK {
			t.Fatal(resp.Message)
		}
	}
}

func TestMenuHyprlandDispatch(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		t.Run(provider, func(t *testing.T) {
			calls := mockHyprctl(t, "configProvider: "+provider)
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cfg := envConfig{backend: "hyprland", self: self, hyprWorkspace: "wisp"}
			t.Setenv("WISP_TEST_SPAWNED", `[{"address":"0x123","class":"wisp-palette","mapped":true,"floating":true,"size":[900,600],"workspace":{"name":"special:wisp"}}]`)
			if err := runDirect(cfg, []string{"menu"}); err != nil {
				t.Fatal(err)
			}
			got := calls()
			if len(got) < 3 || got[2][0] != "dispatch" || !strings.Contains(strings.Join(got[2], " "), "_palette") || !strings.Contains(strings.Join(got[2], " "), "Wisp Menu") || !strings.Contains(strings.Join(got[2], " "), "wisp-palette") {
				t.Fatalf("menu did not preserve terminal palette identity and launch dispatch: %q", got)
			}
		})
	}
}

func TestPaletteLogsActionFailureBeforeRetry(t *testing.T) {
	dir := t.TempDir()
	// Stub fzf emits one selection without any UI or inherited option parsing.
	if err := os.WriteFile(filepath.Join(dir, "fzf"), []byte("#!/bin/sh\nprintf 'broken\\tBroken\\texec\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := envConfig{actionsPath: filepath.Join(dir, "actions.toml"), logPath: filepath.Join(dir, "palette.log")}
	catalog := "[[action]]\nid = \"broken\"\nkind = \"exec\"\ncommand = [\"wisp-nonexistent-palette-test-command\"]\n"
	if err := os.WriteFile(cfg.actionsPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("WISP_NO_DAEMON", "1")
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	stdin, stderr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = input, output
	defer func() { os.Stdin, os.Stderr = stdin, stderr }()
	err = paletteBody(cfg)
	if err == nil || !strings.Contains(err.Error(), `menu action "broken"`) || !strings.Contains(err.Error(), "command not found") {
		t.Fatalf("expected selected action error: %v", err)
	}
	log, readErr := os.ReadFile(cfg.logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(log), "palette error: "+err.Error()) {
		t.Fatalf("action failure missing from log: %s", log)
	}
	retry, readErr := os.ReadFile(output.Name())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(retry), err.Error()) || !strings.Contains(string(retry), "Press Enter to retry") {
		t.Fatalf("lost visible retry error: %s", retry)
	}
}

func TestSummonFocusFailureDoesNotLaunchDuplicate(t *testing.T) {
	calls := mockHyprctl(t, "configProvider: lua")
	t.Setenv("WISP_TEST_FAILURE", "focus")
	t.Setenv("WISP_TEST_CLIENTS", `[{"address":"0x123","class":"wisp-palette","mapped":true,"floating":true,"size":[900,600],"title":"wisp:palette:existing","workspace":{"name":"special:wisp"}}]`)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = launchInvocation(envConfig{backend: "hyprland"}, invocation{Mode: "summon", ID: "palette", Command: self})
	assertHyprctlError(t, err)
	got := calls()
	for _, call := range got {
		if strings.Contains(strings.Join(call, " "), "exec_cmd") {
			t.Fatalf("unexpected spawn after focus error: %q", got)
		}
	}
}

func TestFocusExistingHyprlandNoMatch(t *testing.T) {
	calls := mockHyprctl(t, "configProvider: lua")
	t.Setenv("WISP_TEST_CLIENTS", `[{"address":"0x123","title":"unrelated"}]`)
	for _, mode := range []string{"run", "summon"} {
		focused, err := focusExistingHyprland(invocation{Mode: mode, SurfaceID: "palette"})
		if focused || err != nil {
			t.Fatalf("mode=%s: focused=%v err=%v", mode, focused, err)
		}
	}
	if got := calls(); !reflect.DeepEqual(got, [][]string{{"-j", "systeminfo"}, {"-j", "clients"}}) {
		t.Fatalf("unexpected dispatch without a matching summon window: %q", got)
	}
}

func TestLuaQuote(t *testing.T) {
	input := "'\"\\\n\r\t\x001\x017\x7f雪 $(bad) ]] --"
	want := `"'\"\\\010\013\009\0001\0017\127雪 $(bad) ]] --"`
	if got := luaQuote(input); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if got := luaQuote(""); got != `""` {
		t.Fatalf("empty: %s", got)
	}
}

func TestShellJoinHostileArguments(t *testing.T) {
	args := []string{"", "a'b\"c\\d\n7", "$(exit 99); `exit 98`", "雪"}
	out, err := exec.Command("/bin/sh", "-c", "printf '%s\\000' "+shellJoin(args)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Join(args, "\x00") + "\x00"; string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}
