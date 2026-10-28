package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prefixCatalog(t *testing.T) envConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "actions.toml")
	const catalog = `[[action]]
id = 'notes'
key = 'n'
title = 'Notes'
[[action]]
id = 'reserved-space'
key = 'space'
title = 'Search'
[[action]]
id = 'reserved-mixed-space'
key = ' SpAcE '
title = 'Custom Space'
`
	if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	return envConfig{actionsPath: path}
}

func TestNativeHintsSkipReservedSpace(t *testing.T) {
	cfg := prefixCatalog(t)
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			got := hintTextForPlatform(cfg, platform)
			lower := strings.ToLower(got)
			if strings.Contains(lower, "space") || strings.Contains(lower, "search") || strings.Contains(lower, "hide overlay") {
				t.Fatalf("removed menu option advertised: %q", got)
			}
			for _, want := range []string{"n    Notes", "esc  cancel"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q: %q", want, got)
				}
			}
		})
	}
}

func capturePrefixBindings(t *testing.T, cfg envConfig, backend string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "bindings")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stdout := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = stdout }()
	if err := bindings(cfg, backend); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNativePrefixBindingsConsumeSpace(t *testing.T) {
	cfg := prefixCatalog(t)
	for _, tc := range []struct {
		backend, space string
		want           []string
	}{
		{"aerospace", "space = 'mode wisp'", []string{
			"alt-space = ['exec-and-forget wisp hint show', 'mode wisp']",
			"esc = ['exec-and-forget wisp hint close', 'mode main']",
			"n = ['exec-and-forget wisp hint close', 'exec-and-forget wisp action notes', 'mode main']",
		}},
		{"hyprland", "bind = , SPACE, submap, wisp", []string{
			"bind = ALT, SPACE, exec, wisp hint show",
			"bind = ALT, SPACE, submap, wisp",
			"bind = , ESCAPE, exec, wisp hint close",
			"bind = , ESCAPE, submap, reset",
			"bind = , N, exec, sh -lc 'wisp hint close; wisp action notes'",
			"bind = , N, submap, reset",
			"submap = reset",
		}},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			got := capturePrefixBindings(t, cfg, tc.backend)
			if strings.Contains(got, "palette") || strings.Contains(got, "reserved-") {
				t.Fatalf("unexpected palette or reserved action dispatch: %s", got)
			}
			spaceCount := 0
			for _, line := range strings.Split(got, "\n") {
				if strings.HasPrefix(line, "space =") || strings.HasPrefix(line, "bind = , SPACE,") {
					spaceCount++
					if line != tc.space {
						t.Fatalf("Space must only consume and stay in prefix mode: %q", line)
					}
				}
			}
			if spaceCount != 1 {
				t.Fatalf("got %d Space bindings, want one: %s", spaceCount, got)
			}
			for _, want := range tc.want {
				if !strings.Contains("\n"+got, "\n"+want+"\n") {
					t.Fatalf("missing binding %q: %s", want, got)
				}
			}
		})
	}
}
