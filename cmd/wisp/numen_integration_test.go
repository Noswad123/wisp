package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNumenActionIntegration(t *testing.T) {
	binary := os.Getenv("NUMEN_TEST_BINARY")
	if binary == "" {
		t.Skip("set NUMEN_TEST_BINARY to an absolute Numen executable to test wisp-action generation and temporary installation")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("NUMEN_TEST_BINARY must be an absolute executable path")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatal("NUMEN_TEST_BINARY must name a regular executable file")
	}

	// Keep the private socket path below Unix socket limits, even on macOS.
	dir, err := os.MkdirTemp("/tmp", "wn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "socket ' 雪\".sock")
	previewPath := filepath.Join(dir, "preview ' 雪\".toml")
	installPath := filepath.Join(dir, "installed ' 雪\".toml")
	wantCommand := []string{binary, "--socket", socket}
	// No PATH lookup, inherited Wisp metadata, or real home/config is needed.
	t.Setenv("PATH", dir)
	runNumen := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"--socket", socket, "wisp-action"}, args...)...)
		cmd.Dir = dir
		cmd.Env = []string{"HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_CACHE_HOME=" + dir, "XDG_RUNTIME_DIR=" + dir, "PATH=" + dir}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("numen wisp-action %q: %v (context: %v)\nstderr: %s", args, err, ctx.Err(), &stderr)
		}
		return output
	}
	checkCatalog := func(path string) {
		t.Helper()
		actions, err := loadActions(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("actions = %+v, want exactly one Numen action", actions)
		}
		a := actions[0]
		if a.ID != "numen" || a.Title != "Numen" || a.Kind != "exec" || a.Key != "n" || a.Layout != "" || a.TerminalRows != nil {
			t.Fatalf("unexpected Numen action metadata: %+v", a)
		}
		if !reflect.DeepEqual(a.Command, wantCommand) {
			t.Fatalf("command = %q, want %q", a.Command, wantCommand)
		}
		if !filepath.IsAbs(a.Command[0]) {
			t.Fatalf("command executable is not absolute: %q", a.Command[0])
		}
		cfg := envConfig{actionsPath: path}
		inv, err := invocationFromAction(cfg, "numen")
		if err != nil {
			t.Fatal(err)
		}
		if err := resolveInvocation(cfg, &inv); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(inv.OriginalArgs, wantCommand) || inv.Command != binary || !reflect.DeepEqual(inv.Args, wantCommand[1:]) {
			t.Fatalf("resolved command = %q %q (original %q), want %q", inv.Command, inv.Args, inv.OriginalArgs, wantCommand)
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		// Leading --socket must not become a target path or change workdir.
		if inv.TargetPath != "" || inv.WorkDir != cwd {
			t.Fatalf("target = %q, workdir = %q; want no target and cwd %q", inv.TargetPath, inv.WorkDir, cwd)
		}
		if inv.ID != "numen" || inv.Title != "Numen" || inv.Mode != "exec" || inv.SurfaceID != "numen" || inv.WindowTitle != "wisp:numen: Numen" || inv.Layout != "floating" || inv.TerminalMode || inv.TerminalRows != 0 {
			t.Fatalf("unexpected resolved invocation: %+v", inv)
		}
	}

	preview := runNumen()
	if err := os.WriteFile(previewPath, preview, 0o600); err != nil {
		t.Fatal(err)
	}
	checkCatalog(previewPath)
	runNumen("--install", "--file", installPath)
	checkCatalog(installPath)
	installed, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	runNumen("--install", "--file", installPath)
	after, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, after) {
		t.Fatal("repeated installation changed the catalog")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(entries) {
		t.Fatal("repeated installation changed fixture directory entries")
	}
	for i := range before {
		if before[i].Name() != entries[i].Name() {
			t.Fatal("repeated installation changed fixture directory entries")
		}
	}
	checkCatalog(installPath)
}
