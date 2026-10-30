package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSampleAerospaceActionWithoutShellPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// GUI launches need not inherit the PATH additions from interactive shells.
	t.Setenv("PATH", t.TempDir())
	command := filepath.Join(home, ".local", "lib", "aeros")
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := envConfig{actionsPath: filepath.Join(home, "actions.toml")}
	if err := os.WriteFile(cfg.actionsPath, []byte(sampleActions()), 0o644); err != nil {
		t.Fatal(err)
	}
	inv, err := invocationFromAction(cfg, "aero")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Command != command || len(inv.Args) != 0 {
		t.Fatalf("expected direct selector command %q, got %q %q", command, inv.Command, inv.Args)
	}
	if err := resolveInvocation(cfg, &inv); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(inv.OriginalArgs[0], inv.OriginalArgs[1:]...).Run(); err != nil {
		t.Fatalf("launch selector without interactive shell PATH: %v", err)
	}
}
