package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mockAerospaceCommands(t *testing.T) func() [][]string {
	t.Helper()
	dir := t.TempDir()
	// Shell builtins keep deadline tests independent of Go subprocess startup.
	const body = `
printf '%s\n' "$name" "$@" '--WISP-END--' >> "$WISP_TEST_AERO_LOG"
if [ "$name" = open ]; then
  [ "$WISP_TEST_OPEN_SLOW" != 1 ] || exec /bin/sleep 10
  [ "$WISP_TEST_OPEN_FAIL" != 1 ] || exit 1
  exit 0
fi
if [ "$1" = list-windows ]; then
  [ "$WISP_TEST_AERO_SLOW" != list-windows ] || exec /bin/sleep 10
  [ "$WISP_TEST_AERO_FAIL" != list-windows ] || exit 1
  if [ "$WISP_TEST_AERO_DELAY" = 1 ] && [ ! -f "$WISP_TEST_AERO_LOG.seen" ]; then
    : > "$WISP_TEST_AERO_LOG.seen"
    printf '%s\n' '7|net.kovidgoyal.kitty|original focused terminal'
  else
    printf '%s' "$WISP_TEST_AERO_WINDOWS"
  fi
else
  # Focus may have changed since enumeration; only an explicit ID is safe.
  [ "$3" = --window-id ] && [ "$4" = 42 ] || exit 2
  [ "$WISP_TEST_AERO_SLOW" != mutation ] || exec /bin/sleep 10
  [ "$WISP_TEST_AERO_FAIL" != mutation ] || exit 1
fi
`
	for _, name := range []string{"aerospace", "open", "kitty"} {
		script := "#!/bin/sh\nname=" + name + "\n" + body
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "calls")
	t.Setenv("PATH", dir)
	t.Setenv("WISP_TEST_AERO_LOG", log)
	for _, key := range []string{"WISP_TEST_OPEN_SLOW", "WISP_TEST_OPEN_FAIL", "WISP_TEST_AERO_SLOW", "WISP_TEST_AERO_FAIL", "WISP_TEST_AERO_DELAY", "WISP_TEST_AERO_WINDOWS"} {
		t.Setenv(key, "")
	}
	// Warm newly created executables before measuring production deadlines.
	// macOS can delay their first execution independently of command behavior.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, args := range [][]string{{"open"}, {"aerospace", "list-windows"}} {
		if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil {
			t.Fatalf("prepare mock %s: %v", args[0], err)
		}
	}
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return func() [][]string {
		t.Helper()
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		var calls [][]string
		for _, record := range strings.Split(string(data), "--WISP-END--\n") {
			if record != "" {
				calls = append(calls, strings.Split(strings.TrimSuffix(record, "\n"), "\n"))
			}
		}
		return calls
	}
}

func TestPostLaunchAerospaceLayout(t *testing.T) {
	const title = "wisp:numen: Numen | chat"
	const match = "42|net.kovidgoyal.kitty|" + title + "\n"
	for _, tc := range []struct {
		name, windows, layout, slow, fail string
		delay, mutate                     bool
	}{
		{name: "delayed viewer original remains focused", windows: match, delay: true, mutate: true},
		{name: "focus changes before floating mutation", windows: match, mutate: true},
		{name: "explicit fullscreen", windows: match, layout: "fullscreen", mutate: true},
		{name: "wrong title", windows: "42|net.kovidgoyal.kitty|other\n"},
		{name: "title substring", windows: "42|net.kovidgoyal.kitty|prefix " + title + " suffix\n"},
		{name: "wrong bundle", windows: "42|other|" + title + "\n"},
		{name: "bundle substring", windows: "42|prefix.net.kovidgoyal.kitty|" + title + "\n"},
		{name: "duplicate", windows: match + "43|net.kovidgoyal.kitty|" + title + "\n"},
		{name: "duplicate same id", windows: match + match},
		{name: "malformed", windows: match + "broken\n"},
		{name: "scanner error", windows: match + strings.Repeat("x", 70*1024)},
		{name: "malformed unrelated ID", windows: match + "bad|other|other\n"},
		{name: "empty"},
		{name: "slow enumeration", windows: match, slow: "list-windows"},
		{name: "enumeration failure", windows: match, fail: "list-windows"},
		{name: "mutation failure no fallback", windows: match, fail: "mutation", mutate: true},
		{name: "slow mutation", windows: match, slow: "mutation", mutate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			focusedID := "7"
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{"aerospace"}, args...))
				kind := "mutation"
				if args[0] == "list-windows" {
					kind = "list-windows"
				}
				if tc.slow == kind {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if tc.fail == kind {
					return nil, fmt.Errorf("mock %s failure", kind)
				}
				if kind == "list-windows" {
					if tc.delay && len(calls) == 1 {
						return []byte("7|net.kovidgoyal.kitty|original focused terminal\n"), nil
					}
					// Focus switches between lookup and mutation, away from the viewer.
					focusedID = "99"
					return []byte(tc.windows), nil
				}
				if args[len(args)-2] != "--window-id" || args[len(args)-1] == focusedID {
					t.Fatalf("mutation targets focus instead of viewer: %q", args)
				}
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			start := time.Now()
			correctAerospaceLayout(ctx, invocation{WindowTitle: title, Layout: tc.layout}, run)
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("deadline exceeded: %v", elapsed)
			}
			var mutations [][]string
			for _, call := range calls {
				if call[1] == "list-windows" {
					want := []string{"aerospace", "list-windows", "--all", "--format", "%{window-id}|%{app-bundle-id}|%{window-title}"}
					if !reflect.DeepEqual(call, want) {
						t.Fatalf("unsafe enumeration: %q", call)
					}
				} else {
					mutations = append(mutations, call)
				}
			}
			var want [][]string
			if tc.mutate {
				want = [][]string{{"aerospace", "layout", "floating", "--window-id", "42"}}
				if tc.layout == "fullscreen" {
					want = [][]string{{"aerospace", "fullscreen", "on", "--window-id", "42"}}
				}
			}
			if !reflect.DeepEqual(mutations, want) {
				t.Fatalf("mutations = %q, want %q", mutations, want)
			}
			if tc.delay && len(calls) < 3 {
				t.Fatal("did not wait for delayed viewer")
			}
		})
	}
	for _, id := range []string{"", "0", "-1", "+42", "4.2", " 42", "42 ", "abc", "18446744073709551616"} {
		t.Run("invalid ID "+id, func(t *testing.T) {
			var calls [][]string
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args)
				return []byte(id + "|net.kovidgoyal.kitty|" + title + "\n"), nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			correctAerospaceLayout(ctx, invocation{WindowTitle: title}, run)
			if len(calls) != 1 || calls[0][0] != "list-windows" {
				t.Fatalf("invalid ID caused mutation: %q", calls)
			}
		})
	}
}

func TestMacKittyArgumentsAndChild(t *testing.T) {
	calls := mockAerospaceCommands(t)
	inv := invocation{WorkDir: t.TempDir(), SurfaceID: "numen", Mode: "run", WindowTitle: "wisp:numen: Numen", Layout: "floating", TerminalRows: 28, OriginalArgs: []string{"/bin/sh", "-c", `printf '%s\n' "$WISP|$WISP_ID|$WISP_ROLE|$WISP_TITLE|$WISP_LAYOUT" "$PWD" "$1"`, "sh", "literal ' ; $(false)"}}
	if err := launchMacKitty(envConfig{kittyInitialWidth: "70c", kittyInitialHt: "38c"}, inv); err != nil {
		t.Fatal(err)
	}
	got := calls()[0]
	if !reflect.DeepEqual(got[:3], []string{"open", "-na", "kitty"}) {
		t.Fatal(got)
	}
	var child int
	for i, arg := range got {
		if arg == "/bin/zsh" {
			child = i
		}
	}
	wantPrefix := []string{"open", "-na", "kitty", "--args", "--detach=no", "--single-instance=no", "--override", "macos_quit_when_last_window_closed=yes", "--title", inv.WindowTitle, "--override", "hide_window_decorations=no", "--override", "remember_window_size=no", "--override", "initial_window_width=70c", "--override", "initial_window_height=28c"}
	if child == 0 || !reflect.DeepEqual(got[:child], wantPrefix) {
		t.Fatalf("Kitty arguments changed: %q", got)
	}
	script := got[child+2]
	if strings.Contains(script, "aerospace") || strings.Contains(script, "sleep") {
		t.Fatalf("desktop mutation in child: %s", script)
	}
	wantTail := append([]string{"wisp", inv.WorkDir, inv.SurfaceID, inv.Mode, inv.WindowTitle, inv.Layout}, inv.OriginalArgs...)
	if !reflect.DeepEqual(got[child+3:], wantTail) {
		t.Fatalf("literal argv changed: %q", got[child+3:])
	}
	// Run only the extracted shell fragment, never Kitty or open.
	cmd := exec.Command("/bin/sh", append([]string{"-c", script}, wantTail...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := "1|numen|run|wisp:numen: Numen|floating\n" + inv.WorkDir + "\nliteral ' ; $(false)\n"
	if string(out) != wantOutput {
		t.Fatalf("child output = %q, want %q", out, wantOutput)
	}
	wantTail[1] = filepath.Join(inv.WorkDir, "missing")
	cmd = exec.Command("/bin/sh", append([]string{"-c", script}, wantTail...)...)
	if out, err := cmd.Output(); err == nil || len(out) != 0 {
		t.Fatalf("exec continued after failed cd: %q, %v", out, err)
	}
}

func TestAerospaceDirectLaunchWaits(t *testing.T) {
	calls := mockAerospaceCommands(t)
	t.Setenv("WISP_NO_DAEMON", "1")
	t.Setenv("WISP_TEST_AERO_DELAY", "1")
	t.Setenv("WISP_TEST_AERO_WINDOWS", "42|net.kovidgoyal.kitty|wisp:numen: Numen\n")
	inv := invocation{Mode: "run", ID: "numen", Title: "Numen", Command: "/bin/sh", WorkDir: t.TempDir()}
	start := time.Now()
	if err := launchInvocation(envConfig{backend: "aerospace"}, inv); err != nil {
		t.Fatal(err)
	}
	got := calls()
	want := []string{"aerospace", "layout", "floating", "--window-id", "42"}
	if len(got) != 4 || !reflect.DeepEqual(got[len(got)-1], want) || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("launch returned before correction: %q", got)
	}
}

func TestMacKittyOpenFailureAndDeadline(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprint(slow), func(t *testing.T) {
			calls := mockAerospaceCommands(t)
			if slow {
				t.Setenv("WISP_TEST_OPEN_SLOW", "1")
			} else {
				t.Setenv("WISP_TEST_OPEN_FAIL", "1")
			}
			start := time.Now()
			if err := launchInvocation(envConfig{backend: "aerospace"}, invocation{Mode: "run", Command: "/bin/sh", WorkDir: t.TempDir()}); err == nil {
				t.Fatal("open failure not returned")
			}
			if time.Since(start) > 4*time.Second || len(calls()) != 1 {
				t.Fatalf("open not bounded or correction ran after failure: %q", calls())
			}
		})
	}
}
