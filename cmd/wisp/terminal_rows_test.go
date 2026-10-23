package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActionTerminalRows(t *testing.T) {
	for _, tc := range []struct {
		name, value, kind, layout string
		rows                      int
		wantErr                   string
	}{
		{name: "absent", kind: "summon"},
		{name: "positive", value: "28", kind: "summon", rows: 28},
		{name: "shell", value: "28", kind: "shell", rows: 28},
		{name: "run", value: "28", kind: "run", rows: 28},
		{name: "zero", value: "0", kind: "summon", wantErr: "positive integer"},
		{name: "negative", value: "-1", kind: "summon", wantErr: "positive integer"},
		{name: "fraction", value: "28.5", kind: "summon", wantErr: "terminal_rows"},
		{name: "string", value: `"28"`, kind: "summon", wantErr: "terminal_rows"},
		{name: "exec", value: "28", kind: "exec", wantErr: "only supported"},
		{name: "fullscreen", value: "28", kind: "summon", layout: "fullscreen", wantErr: "requires floating"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "actions.toml")
			catalog := fmt.Sprintf("[[action]]\nid='test'\nkind=%q\nlayout=%q\ncommand=['true']\n", tc.kind, tc.layout)
			if tc.value != "" {
				catalog += "terminal_rows=" + tc.value + "\n"
			}
			if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
				t.Fatal(err)
			}
			inv, err := invocationFromAction(envConfig{actionsPath: path, shell: "/bin/sh"}, "test")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v, want %q", err, tc.wantErr)
				}
				// The direct action path must reject it before launching anything.
				if err := launchActionDirect(envConfig{actionsPath: path}, "test"); err == nil {
					t.Fatal("invalid action launched")
				}
				return
			}
			if err != nil || inv.TerminalRows != tc.rows {
				t.Fatalf("inv=%+v err=%v", inv, err)
			}
		})
	}
}

func TestTerminalRowsInvocationValidation(t *testing.T) {
	for _, inv := range []invocation{
		{Mode: "summon", TerminalRows: -1},
		{Mode: "exec", TerminalRows: 28},
		{Mode: "run", Layout: "fullscreen", TerminalRows: 28},
	} {
		if err := launchInvocation(envConfig{}, inv); err == nil || !strings.Contains(err.Error(), "terminal_rows") {
			t.Fatalf("expected terminal_rows error, got %v", err)
		}
	}
}

func TestTerminalRowsSpawnRules(t *testing.T) {
	for _, provider := range []string{"lua", "hyprlang"} {
		t.Run(provider, func(t *testing.T) {
			calls := mockHyprctl(t, "State:\nconfigProvider: "+provider)
			inv := invocation{Mode: "summon", SurfaceID: "custom", Layout: "floating", TerminalRows: 28}
			if err := launchHyprlandKitty(envConfig{hyprWorkspace: "wisp", kittyInitialHt: "42c"}, inv); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(calls()[1], " ")
			for _, want := range []string{"initial_window_height=28c", "remember_window_size=no", "normal", "silent"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q: %s", want, got)
				}
			}
			if strings.Contains(got, "600") || strings.Contains(got, "42c") {
				t.Fatal(got)
			}
		})
	}
}

func TestKittyHeightPreference(t *testing.T) {
	cfg := envConfig{kittyInitialWidth: "70c", kittyInitialHt: "38c"}
	if kittyHeight(cfg, invocation{}) != "38c" {
		t.Fatal("mac default changed")
	}
	cfg.kittyInitialHt = "42c"
	if kittyHeight(cfg, invocation{}) != "42c" || kittyHeight(cfg, invocation{TerminalRows: 28}) != "28c" {
		t.Fatal("height precedence")
	}
}
