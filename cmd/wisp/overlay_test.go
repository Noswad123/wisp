package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type overlayFake struct {
	c                              *overlayController
	clients                        []hyprClient
	monitors                       []hyprMonitor
	active                         hyprClient
	calls                          []string
	ticks, mapAfter, spawned       int
	clock                          time.Time
	failQuery, failDispatch        string
	mapped                         bool
	ignoreFocus                    bool
	raised                         []string
	requestedSize, constrainedSize [2]int
	activeQueries                  int
	onActiveQuery                  func()
	onFloat                        func(*hyprClient)
}

func fakeOverlay(t *testing.T) *overlayFake {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "fake-state")
	f := &overlayFake{monitors: []hyprMonitor{{Name: "DP-1", Width: 3840, Height: 2160, Scale: 1, Focused: true}}, mapAfter: 1}
	f.c = &overlayController{cfg: envConfig{hyprWorkspace: "wisp"}, lua: true, timeout: 100 * time.Millisecond,
		now: func() time.Time { return f.clock }, sleep: func(d time.Duration) { f.clock = f.clock.Add(d) },
		query: func(name string, dst any) error {
			f.calls = append(f.calls, "query:"+name)
			if name == f.failQuery {
				return errors.New("query socket failed")
			}
			var value any
			switch name {
			case "clients":
				if f.spawned > 0 {
					f.ticks++
					if !f.mapped && f.ticks >= f.mapAfter {
						f.clients = []hyprClient{testSurface()}
						f.mapped = true
					}
				}
				value = f.clients
			case "monitors":
				value = f.monitors
			case "activewindow":
				f.activeQueries++
				if f.onActiveQuery != nil {
					f.onActiveQuery()
				}
				value = f.active
			default:
				t.Fatalf("unknown query %s", name)
			}
			data, _ := json.Marshal(value)
			return json.Unmarshal(data, dst)
		},
		spawn: func(inv invocation) error { f.calls = append(f.calls, "spawn"); f.spawned++; return nil },
		dispatch: func(lua bool, name, arg, expr string) error {
			f.calls = append(f.calls, name+":"+arg+":"+expr)
			if f.failDispatch != "" && (strings.Contains(expr, f.failDispatch) || strings.Contains(name, f.failDispatch)) {
				return errors.New("dispatcher failed: details")
			}
			if name == "togglespecialworkspace" {
				if f.monitors[0].Special.Name == "" {
					f.monitors[0].Special.Name = "special:wisp"
				} else {
					f.monitors[0].Special.Name = ""
				}
			}
			if name == "focusmonitor" {
				for i := range f.monitors {
					f.monitors[i].Focused = f.monitors[i].Name == arg
				}
			}
			if name == "movetoworkspacesilent" {
				f.clients[0].Workspace.Name = "special:wisp"
			}
			if len(f.clients) > 0 {
				w := &f.clients[0]
				if strings.Contains(expr, "fullscreen_state") || name == "fullscreenstate" {
					w.Fullscreen = 0
				}
				if strings.Contains(expr, "window.fullscreen(") {
					w.Fullscreen = 2
				}
				if name == "fullscreen" {
					w.Fullscreen = 2
				}
				if name == "setfloating" {
					w.Floating = true
					if f.onFloat != nil {
						f.onFloat(w)
					}
				}
				if name == "resizewindowpixel" {
					if _, err := fmt.Sscanf(arg, "exact %d %d,", &f.requestedSize[0], &f.requestedSize[1]); err != nil {
						t.Fatalf("bad resize args %q: %v", arg, err)
					}
					w.Size = f.requestedSize
					if f.constrainedSize != [2]int{} {
						w.Size = f.constrainedSize
					}
				}
				if name == "focuswindow" && !f.ignoreFocus {
					f.active = *w
				}
				if name == "alterzorder" {
					// Model the conservative focus-targeted implementation: an
					// address argument must not be trusted instead of verification.
					f.raised = append(f.raised, f.active.Address)
				}
			}
			return nil
		},
	}
	return f
}

func testSurface() hyprClient {
	return hyprClient{Address: "0x123", Class: "wisp-wyrm", Mapped: true, Floating: true, Size: [2]int{900, 600}, Workspace: hyprWorkspace{Name: "special:wisp"}}
}
func testInvocation() invocation {
	return invocation{Mode: "summon", SurfaceID: "wyrm", Layout: "floating"}
}

func TestTerminalRowsInitialGeometry(t *testing.T) {
	for _, lua := range []bool{false, true} {
		for _, adopted := range []bool{false, true} {
			for _, monitorHeight := range []int{2160, 700} {
				t.Run(fmt.Sprintf("lua=%v/adopted=%v/height=%d", lua, adopted, monitorHeight), func(t *testing.T) {
					f := fakeOverlay(t)
					f.c.lua = lua
					f.monitors[0].Height = monitorHeight
					w := testSurface()
					w.Size = [2]int{1100, 820}
					if adopted {
						w.Floating, w.Fullscreen, w.Size = false, 2, [2]int{3840, 2160}
						// Normal dimensions arrive only after fullscreen/tile removal.
						f.onFloat = func(w *hyprClient) { w.Size = [2]int{1100, 820} }
					}
					f.clients = []hyprClient{w}
					inv := testInvocation()
					inv.TerminalRows = 28
					if err := f.c.present(w, inv, !adopted); err != nil {
						t.Fatal(err)
					}
					if want := [2]int{900, min(820, monitorHeight)}; f.requestedSize != want {
						t.Fatalf("size=%v want=%v", f.requestedSize, want)
					}
					// Preference is initial only, even if user later shrinks below it.
					f.onFloat = nil
					f.clients[0].Size = [2]int{740, 450}
					f.requestedSize = [2]int{}
					if err := f.c.summon(f.clients[0], inv); err != nil {
						t.Fatal(err)
					}
					if f.requestedSize != [2]int{} || f.active.Size != [2]int{740, 450} {
						t.Fatal("summon changed custom geometry")
					}
				})
			}
		}
	}
}

func TestTerminalRowsLateMaximizeKeepsMeasuredHeight(t *testing.T) {
	for _, lua := range []bool{false, true} {
		t.Run(fmt.Sprintf("lua=%v", lua), func(t *testing.T) {
			f := fakeOverlay(t)
			f.c.lua = lua
			w := testSurface()
			w.Size[1] = 820
			f.clients = []hyprClient{w}
			inv := testInvocation()
			inv.TerminalRows = 28
			f.onActiveQuery = func() {
				if f.activeQueries == 3 {
					simulateStartupMaximize(f)
				}
			}
			if err := f.c.present(w, inv, true); err != nil {
				t.Fatal(err)
			}
			if f.active.Fullscreen != 0 || !f.active.Floating || f.active.Size != [2]int{900, 820} {
				t.Fatalf("late maximize lost measured height: %+v", f.active)
			}
			if got := strings.Count(strings.Join(f.calls, "\n"), "resizewindowpixel:"); got != 2 {
				t.Fatalf("expected initial sizing and bounded correction, got %d", got)
			}
		})
	}
}

func TestTerminalRowsLaunchHandoff(t *testing.T) {
	f := fakeOverlay(t)
	f.c.spawn = func(inv invocation) error {
		if inv.TerminalRows != 28 {
			t.Fatalf("spawn lost terminal_rows: %+v", inv)
		}
		w := testSurface()
		w.Size[1] = 820
		f.clients = []hyprClient{w}
		return nil
	}
	inv := testInvocation()
	inv.TerminalRows = 28
	if err := f.c.launch(inv); err != nil {
		t.Fatal(err)
	}
	if f.active.Size != [2]int{900, 820} {
		t.Fatalf("launch lost initial height: %+v", f.active)
	}
	if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch guard not cleared: %v", err)
	}
}

func TestOverlayAdoptsFormerFullscreenWithFloatingGeometry(t *testing.T) {
	f := fakeOverlay(t)
	w := testSurface()
	w.Floating = false
	w.Fullscreen = 2
	w.Size = [2]int{3840, 2160}
	f.clients = []hyprClient{w}
	if err := f.c.summon(w, testInvocation()); err != nil {
		t.Fatal(err)
	}
	if f.requestedSize != [2]int{900, 600} || !f.active.Floating || f.active.Fullscreen != 0 {
		t.Fatalf("old fullscreen layout was not normalized: size=%v active=%+v", f.requestedSize, f.active)
	}
}

func simulateStartupMaximize(f *overlayFake) {
	f.active.Fullscreen = 1
	f.active.Floating = true
	f.active.Size = [2]int{2556, 1402}
	for i := range f.clients {
		if f.clients[i].Address == f.active.Address {
			f.clients[i] = f.active
		}
	}
}

func TestInitialPresentationReconcilesLateClientMaximize(t *testing.T) {
	for _, lua := range []bool{true, false} {
		t.Run(fmt.Sprintf("lua=%v", lua), func(t *testing.T) {
			f := fakeOverlay(t)
			f.c.lua = lua
			restored := false
			f.onActiveQuery = func() {
				// First: focus verification. Second: initially correct snapshot.
				// Third: Kitty's delayed maximize request after the first setter.
				if f.activeQueries == 3 {
					restored = true
					simulateStartupMaximize(f)
				}
			}
			if err := f.c.launch(testInvocation()); err != nil {
				t.Fatal(err)
			}
			if !restored {
				t.Fatal("handoff accepted first snapshot before late startup request")
			}
			if !f.active.Floating || f.active.Fullscreen != 0 || f.active.Size != [2]int{900, 600} {
				t.Fatalf("startup restoration won: %+v", f.active)
			}
			if got := strings.Count(strings.Join(f.calls, "\n"), "resizewindowpixel:"); got != 2 {
				t.Fatalf("expected initial sizing plus one observed correction, got %d", got)
			}
			if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pending not cleared: %v", err)
			}
		})
	}
}

func TestInitialPresentationCapsConflictingClientRequests(t *testing.T) {
	f := fakeOverlay(t)
	f.onActiveQuery = func() {
		if f.activeQueries >= 2 {
			simulateStartupMaximize(f)
		}
	}
	err := f.c.launch(testInvocation())
	if err == nil || !strings.Contains(err.Error(), "did not settle after 3 corrections") {
		t.Fatalf("expected bounded conflict error: %v", err)
	}
	if got := strings.Count(strings.Join(f.calls, "\n"), "resizewindowpixel:"); got != 4 {
		t.Fatalf("unbounded corrections: %d", got)
	}
	if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); err != nil {
		t.Fatalf("lost uncertain guard: %v", err)
	}
}

func TestInitialReconciliationDoesNotForceAnotherFocusedSurface(t *testing.T) {
	f := fakeOverlay(t)
	f.onActiveQuery = func() {
		if f.activeQueries >= 2 {
			f.active.Address = "0xunrelated"
			f.active.Fullscreen = 1
		}
	}
	if err := f.c.launch(testInvocation()); err == nil {
		t.Fatal("expected lost-focus readiness error")
	}
	if got := strings.Count(strings.Join(f.calls, "\n"), "resizewindowpixel:"); got != 1 {
		t.Fatalf("corrected a different focused surface: %d", got)
	}
	for _, address := range f.raised {
		if address != "0x123" {
			t.Fatalf("raised unrelated surface: %s", address)
		}
	}
}

func TestInitialReconciliationReturnsSetterErrors(t *testing.T) {
	for _, lua := range []bool{true, false} {
		t.Run(fmt.Sprintf("lua=%v", lua), func(t *testing.T) {
			f := fakeOverlay(t)
			f.c.lua = lua
			f.onActiveQuery = func() {
				if f.activeQueries == 2 {
					simulateStartupMaximize(f)
					if lua {
						f.failDispatch = "fullscreen_state"
					} else {
						f.failDispatch = "fullscreenstate"
					}
				}
			}
			if err := f.c.launch(testInvocation()); err == nil || !strings.Contains(err.Error(), "dispatcher failed") {
				t.Fatalf("lost correction error: %v", err)
			}
		})
	}
}

func TestOverlayShowHideIdempotent(t *testing.T) {
	f := fakeOverlay(t)
	for _, show := range []bool{true, true, false, false} {
		if err := f.c.visibility(show); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(strings.Join(f.calls, "\n"), "togglespecialworkspace:"); got != 2 {
		t.Fatalf("toggles=%d: %v", got, f.calls)
	}
}

func TestOverlayVisibleOnOtherMonitor(t *testing.T) {
	f := fakeOverlay(t)
	f.monitors = append(f.monitors, hyprMonitor{Name: "DP-2", Special: hyprWorkspace{Name: "special:wisp"}})
	if err := f.c.visibility(true); err != nil {
		t.Fatal(err)
	}
	if !f.monitors[1].Focused || strings.Contains(strings.Join(f.calls, "\n"), "togglespecialworkspace:") {
		t.Fatal(f.calls)
	}
}

func TestOverlayMapBeforeHandoff(t *testing.T) {
	f := fakeOverlay(t)
	f.mapAfter = 3
	if err := f.c.launch(testInvocation()); err != nil {
		t.Fatal(err)
	}
	if f.ticks < 3 || f.active.Address == "" {
		t.Fatal("returned before mapped and focused")
	}
	spawn := -1
	queries := 0
	for i, call := range f.calls {
		if call == "spawn" {
			spawn = i
		}
		if spawn >= 0 && call == "query:clients" {
			queries++
		}
		if (strings.HasPrefix(call, "focuswindow:") || strings.HasPrefix(call, "togglespecialworkspace:")) && queries < 3 {
			t.Fatalf("premature reveal/focus: %v", f.calls)
		}
	}
}

func TestNewLaunchUsesNewAddressNotExistingClass(t *testing.T) {
	f := fakeOverlay(t)
	old := testSurface()
	old.Address = "0xold"
	f.clients = []hyprClient{old}
	inv := testInvocation()
	inv.Mode = "run"
	if err := f.c.launch(inv); err != nil {
		t.Fatal(err)
	}
	if f.active.Address != "0x123" || f.spawned != 1 {
		t.Fatalf("selected preexisting address: %+v", f.active)
	}
}

func TestSummonReclaimsOnlyIdentifiedWispSurface(t *testing.T) {
	f := fakeOverlay(t)
	w := testSurface()
	w.Workspace.Name = "1"
	w.Title = "changed title"
	f.clients = []hyprClient{w}
	if err := f.c.launch(testInvocation()); err != nil {
		t.Fatal(err)
	}
	if f.spawned != 0 || f.active.Workspace.Name != "special:wisp" {
		t.Fatal(f.calls)
	}
}

func TestLateMapCanRecoverUncertainLaunch(t *testing.T) {
	f := fakeOverlay(t)
	f.mapAfter = 1000
	if err := f.c.launch(testInvocation()); err == nil {
		t.Fatal("expected readiness timeout")
	}
	f.mapAfter = 0
	if err := f.c.launch(testInvocation()); err != nil {
		t.Fatal(err)
	}
	if f.spawned != 1 {
		t.Fatal("duplicated late map")
	}
	if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("guard not cleared: %v", err)
	}
}

func TestLateMapAfterTimeoutAndHideStaysHidden(t *testing.T) {
	for _, lua := range []bool{true, false} {
		t.Run(fmt.Sprintf("lua=%v", lua), func(t *testing.T) {
			f := fakeOverlay(t)
			f.c.lua = lua
			f.mapAfter = 1000
			f.monitors[0].Special.Name = "special:wisp"
			if err := f.c.launch(testInvocation()); err == nil {
				t.Fatal("expected map timeout")
			}
			if err := f.c.visibility(false); err != nil {
				t.Fatal(err)
			}
			before := strings.Count(strings.Join(f.calls, "\n"), "togglespecialworkspace:")
			// The app maps after the launch operation has returned and hide has
			// completed. Merely observing that event must not reveal/focus it.
			f.mapAfter = 0
			var mapped []hyprClient
			if err := f.c.query("clients", &mapped); err != nil {
				t.Fatal(err)
			}
			if len(mapped) != 1 || f.monitors[0].Special.Name != "" || f.active.Address != "" || len(f.raised) != 0 {
				t.Fatal("late map reopened/presented hidden overlay")
			}
			if got := strings.Count(strings.Join(f.calls, "\n"), "togglespecialworkspace:"); got != before {
				t.Fatal("async late reveal")
			}
			if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); err != nil {
				t.Fatalf("lost uncertainty guard: %v", err)
			}
			if err := f.c.visibility(true); err != nil {
				t.Fatal(err)
			}
			if f.monitors[0].Special.Name != "special:wisp" {
				t.Fatal("explicit show failed")
			}
			if err := f.c.launch(testInvocation()); err != nil {
				t.Fatal(err)
			}
			if f.spawned != 1 {
				t.Fatal("duplicated late-mapped surface")
			}
		})
	}
}

func TestFloatingSummonRaisesOnlyAfterExactFocus(t *testing.T) {
	for _, lua := range []bool{true, false} {
		t.Run(fmt.Sprintf("lua=%v", lua), func(t *testing.T) {
			f := fakeOverlay(t)
			f.c.lua = lua
			w := testSurface()
			w.Address = "0x123\"\\\n"
			w.Size = [2]int{1200, 700}
			f.clients = []hyprClient{w}
			f.active = testSurface()
			f.active.Address = "0xunrelated"
			if err := f.c.present(w, testInvocation(), false); err != nil {
				t.Fatal(err)
			}
			if len(f.raised) != 1 || f.raised[0] != w.Address {
				t.Fatalf("raised wrong window: %q", f.raised)
			}
			if f.active.Size != w.Size || f.requestedSize != [2]int{} {
				t.Fatal("summon changed user geometry")
			}
			for i, call := range f.calls {
				if !strings.HasPrefix(call, "alterzorder:") {
					continue
				}
				if i == 0 || f.calls[i-1] != "query:activewindow" {
					t.Fatal("raise was not preceded by observed exact focus")
				}
				if !strings.Contains(call, "top,address:"+w.Address) || !strings.Contains(call, "window = "+luaQuote("address:"+w.Address)) {
					t.Fatalf("untargeted raise: %q", call)
				}
				if !strings.Contains(call, "hl.get_active_window()") || !strings.Contains(call, "w.address ~= "+luaQuote(w.Address)) {
					t.Fatal("missing in-compositor Lua focus guard")
				}
			}
		})
	}
}

func TestRaiseRefusesUncertainFocus(t *testing.T) {
	for _, lua := range []bool{true, false} {
		for _, failure := range []string{"ignored focus", "query", "focus dispatch", "raise dispatch"} {
			t.Run(fmt.Sprintf("lua=%v/%s", lua, failure), func(t *testing.T) {
				f := fakeOverlay(t)
				f.c.lua = lua
				f.clients = []hyprClient{testSurface()}
				f.active = testSurface()
				f.active.Address = "0xunrelated"
				switch failure {
				case "ignored focus":
					f.ignoreFocus = true
				case "query":
					f.failQuery = "activewindow"
				case "focus dispatch":
					f.failDispatch = "hl.dsp.focus("
				case "raise dispatch":
					f.failDispatch = "alter_zorder"
				}
				if err := f.c.focusAndRaise("0x123"); err == nil {
					t.Fatal("expected focus/raise error")
				}
				if len(f.raised) != 0 {
					t.Fatalf("raised despite uncertainty: %q", f.raised)
				}
			})
		}
	}
}

func TestInitialGeometryAcceptsConstrainedSizeAndClearsPending(t *testing.T) {
	for _, lua := range []bool{true, false} {
		t.Run(fmt.Sprintf("lua=%v", lua), func(t *testing.T) {
			f := fakeOverlay(t)
			f.c.lua = lua
			f.constrainedSize = [2]int{800, 500}
			if err := f.c.launch(testInvocation()); err != nil {
				t.Fatal(err)
			}
			if f.requestedSize != [2]int{900, 600} || f.active.Size != [2]int{800, 500} {
				t.Fatalf("requested %v, observed %v", f.requestedSize, f.active.Size)
			}
			if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pending not cleared: %v", err)
			}
		})
	}
}

func TestInitialGeometryClampsToLogicalUsableMonitor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		monitor hyprMonitor
		want    [2]int
	}{
		{"small", hyprMonitor{Width: 800, Height: 500, Scale: 1}, [2]int{800, 500}},
		{"scaled and reserved", hyprMonitor{Width: 1600, Height: 1000, Scale: 2, Reserved: [4]int{10, 20, 30, 40}}, [2]int{760, 440}},
		{"rotated", hyprMonitor{Width: 1000, Height: 1600, Scale: 2, Transform: 1}, [2]int{800, 500}},
		{"fractional scale", hyprMonitor{Width: 1200, Height: 800, Scale: 1.5, Reserved: [4]int{0, 20, 0, 13}}, [2]int{800, 500}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fakeOverlay(t)
			m := tc.monitor
			m.Name = "DP-2"
			m.Special.Name = "special:wisp"
			// Clamp to the monitor actually displaying the overlay, not an
			// arbitrary first/focused monitor's physical pixel dimensions.
			f.monitors = append(f.monitors, m)
			if err := f.c.launch(testInvocation()); err != nil {
				t.Fatal(err)
			}
			if f.requestedSize != tc.want || f.active.Size != tc.want {
				t.Fatalf("got requested=%v active=%v want=%v", f.requestedSize, f.active.Size, tc.want)
			}
			if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pending not cleared: %v", err)
			}
		})
	}
}

func TestInitialGeometryRejectsUnusableObservedSize(t *testing.T) {
	for _, size := range [][2]int{{0, 500}, {800, 0}, {5000, 500}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := fakeOverlay(t)
			f.constrainedSize = size
			if err := f.c.launch(testInvocation()); err == nil {
				t.Fatal("accepted invalid/out-of-monitor geometry")
			}
			if _, err := os.Stat(overlaySessionPath("-wyrm.pending")); err != nil {
				t.Fatal("lost uncertain guard")
			}
		})
	}
}

func TestOverlayConcurrentSummonAndHide(t *testing.T) {
	f := fakeOverlay(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- withOverlayLock(func() error { return f.c.launch(testInvocation()) }) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.spawned != 1 {
		t.Fatalf("spawned %d", f.spawned)
	}
	if err := withOverlayLock(func() error { return f.c.visibility(false) }); err != nil {
		t.Fatal(err)
	}
	if f.monitors[0].Special.Name != "" {
		t.Fatal("launch reopened after hide")
	}
}

func TestOverlayGeometry(t *testing.T) {
	for _, layout := range []string{"floating", "fullscreen"} {
		for _, initial := range []bool{true, false} {
			t.Run(layout+"/"+map[bool]string{true: "initial", false: "summon"}[initial], func(t *testing.T) {
				f := fakeOverlay(t)
				w := testSurface()
				w.Size = [2]int{2556, 1402}
				// Only an already-floating, non-fullscreen summon preserves user
				// geometry; fullscreen/tiled adoption is normalized separately.
				if initial || layout == "fullscreen" {
					w.Fullscreen = 2
				}
				f.clients = []hyprClient{w}
				inv := testInvocation()
				inv.Layout = layout
				if err := f.c.present(w, inv, initial); err != nil {
					t.Fatal(err)
				}
				calls := strings.Join(f.calls, "\n")
				resize := strings.Contains(calls, "resizewindowpixel:")
				if resize != (initial && layout == "floating") {
					t.Fatal(calls)
				}
				if layout == "floating" && !initial && f.active.Size != w.Size {
					t.Fatal("lost user resize")
				}
				if layout == "floating" && !strings.Contains(calls, `action = "enable"`) {
					t.Fatal("float must set, not toggle")
				}
			})
		}
	}
}

func TestHideSerializesWithInflightLaunch(t *testing.T) {
	f := fakeOverlay(t)
	entered, release := make(chan struct{}), make(chan struct{})
	spawn := f.c.spawn
	f.c.spawn = func(inv invocation) error { close(entered); <-release; return spawn(inv) }
	launchDone, hideDone := make(chan error, 1), make(chan error, 1)
	go func() { launchDone <- withOverlayLock(func() error { return f.c.launch(testInvocation()) }) }()
	<-entered
	go func() { hideDone <- withOverlayLock(func() error { return f.c.visibility(false) }) }()
	close(release)
	if err := <-launchDone; err != nil {
		t.Fatal(err)
	}
	if err := <-hideDone; err != nil {
		t.Fatal(err)
	}
	if f.monitors[0].Special.Name != "" {
		t.Fatal("inflight launch reopened after hide completed")
	}
}

func TestPaletteRecoveryNeverRevealsHiddenOverlay(t *testing.T) {
	f := fakeOverlay(t)
	w := testSurface()
	w.Class = "wisp-palette"
	f.clients = []hyprClient{w}
	if err := f.c.restorePalette(); err != nil {
		t.Fatal(err)
	}
	if f.active.Address != "" {
		t.Fatal("focused palette in hidden overlay")
	}
	f.monitors[0].Special.Name = "special:wisp"
	if err := f.c.restorePalette(); err != nil {
		t.Fatal(err)
	}
	if f.active.Address != w.Address {
		t.Fatal("failed to preserve visible palette errors")
	}
	if strings.Contains(strings.Join(f.calls, "\n"), "togglespecialworkspace:") {
		t.Fatal("recovery toggled overlay")
	}
}

func TestOverlayFailureDoesNotDuplicate(t *testing.T) {
	for _, failure := range []string{"query", "focus", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			f := fakeOverlay(t)
			if failure == "query" {
				f.failQuery = "clients"
			}
			if failure == "focus" {
				f.clients = []hyprClient{testSurface()}
				f.failDispatch = "hl.dsp.focus("
			}
			if failure == "timeout" {
				f.mapAfter = 1000
			}
			for range 2 {
				if err := f.c.launch(testInvocation()); err == nil {
					t.Fatal("expected actionable error")
				}
			}
			want := 0
			if failure == "timeout" {
				want = 1
			}
			if f.spawned != want {
				t.Fatalf("spawned %d", f.spawned)
			}
		})
	}
}

func TestWispIdentity(t *testing.T) {
	w := testSurface()
	w.Title = "changed by app"
	if !wispMatch(w, "wyrm") {
		t.Fatal("lost stable class")
	}
	w.Class = "unrelated"
	w.Title = "wisp:wyrm: impostor"
	if wispMatch(w, "wyrm") {
		t.Fatal("stole unrelated window")
	}
	w.Class = "kitty"
	if !wispMatch(w, "wyrm") {
		t.Fatal("lost legacy compatibility")
	}
}

func TestLinuxCatalog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WISP_ACTIONS_PATH", "")
	t.Setenv("WISP_CONFIG_HOME", dir)
	if got := defaultActionsPath("linux", dir); got != filepath.Join(dir, "actions.toml") {
		t.Fatal(got)
	}
	linux := filepath.Join(dir, "actions.linux.toml")
	if err := os.WriteFile(linux, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := defaultActionsPath("linux", dir); got != linux {
		t.Fatal(got)
	}
	if got := defaultActionsPath("darwin", dir); got != filepath.Join(dir, "actions.toml") {
		t.Fatal(got)
	}
	if runtime.GOOS == "linux" && loadEnv().actionsPath != linux {
		t.Fatal("loadEnv ignored Linux catalog")
	}
	t.Setenv("WISP_ACTIONS_PATH", "/explicit.toml")
	if got := loadEnv().actionsPath; got != "/explicit.toml" {
		t.Fatal(got)
	}
}

func TestLinuxLayoutInheritance(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux policy")
	}
	t.Setenv("WISP_LAYOUT", "fullscreen")
	inv, err := parseInvocation(envConfig{}, []string{"run", "--", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Layout != "" {
		t.Fatal("inherited fullscreen")
	}
	inv, err = parseInvocation(envConfig{}, []string{"run", "--layout", "fullscreen", "--", "true"})
	if err != nil || inv.Layout != "fullscreen" {
		t.Fatalf("explicit layout: %v %v", inv, err)
	}
}

func TestOverlayCommandsDelegate(t *testing.T) {
	for _, op := range []string{"show", "hide"} {
		if !shouldDelegate([]string{op}) {
			t.Fatal(op)
		}
	}
	if shouldDelegate(nil) {
		t.Fatal("empty invocation must not summon")
	}
}
