package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func hintFixture(t *testing.T, body string) (envConfig, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux process protocol")
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, "wisp-hint-linux")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return envConfig{self: filepath.Join(dir, "wisp"), hintPIDPath: filepath.Join(dir, "hint.pid")}, helper
}

func TestLinuxHintReadiness(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"ready", "printf 'READY\\n' >&3; exec sleep 30", ""},
		{"failure", "printf 'GTK unavailable' >&2; exit 2", "GTK unavailable"},
		{"timeout", "exec sleep 30", "timeout"},
		{"invalid", "printf 'WRONG\\n' >&3; exec sleep 30", "invalid readiness"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, helper := hintFixture(t, tc.body)
			err := startLinuxHint(cfg, helper, "test", 100*time.Millisecond)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want %s", err, tc.want)
				}
				if _, err := os.Stat(cfg.hintPIDPath); !os.IsNotExist(err) {
					t.Fatalf("failed launch left PID record: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer closeHint(cfg)
			data, err := os.ReadFile(cfg.hintPIDPath)
			if err != nil {
				t.Fatal(err)
			}
			var record hintProcessRecord
			if err := json.Unmarshal(data, &record); err != nil || !record.Native || record.Start == "" {
				t.Fatalf("bad record %s: %v", data, err)
			}
			if err := closeHint(cfg); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if _, err := hintProcessStart(record.PID); os.IsNotExist(err) {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("closed child was not reaped")
		})
	}
}

func TestLinuxHintPIDWriteFailure(t *testing.T) {
	cfg, helper := hintFixture(t, "printf 'READY\\n' >&3; exec sleep 30")
	cfg.hintPIDPath = t.TempDir() // Writing to a directory must fail and reap the child.
	if err := startLinuxHint(cfg, helper, "", time.Second); err == nil || !strings.Contains(err.Error(), "record hint PID") {
		t.Fatalf("got %v", err)
	}
}

func TestLinuxHintRejectsStaleIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ownership")
	}
	for _, data := range []string{
		strconv.Itoa(os.Getpid()),
		`{"pid":` + strconv.Itoa(os.Getpid()) + `,"start_time":"not-the-start-time","native":true}`,
		`{"pid":-1,"start_time":"1"}`,
	} {
		if err := closeLinuxHint([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHintHelperPlatformDiscovery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WISP_HINT_HELPER", "")
	t.Setenv("PATH", dir)
	cfg := envConfig{self: filepath.Join(dir, "wisp")}
	for _, platform := range []string{"linux", "darwin"} {
		name := "wisp-hint-linux"
		if platform == "darwin" {
			name = "wisp-hint-macos"
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := findHintHelperForPlatform(cfg, platform)
		if err != nil || got != path {
			t.Fatalf("%s: %s %v", platform, got, err)
		}
	}
	t.Setenv("WISP_HINT_HELPER", filepath.Join(dir, "override"))
	if _, err := findHintHelperForPlatform(cfg, "linux"); err == nil {
		t.Fatal("missing override silently ignored")
	}
}

func TestHintPlatformText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.toml")
	if err := os.WriteFile(path, []byte("[[action]]\nid='notes'\nkey='n'\ntitle='Linux Notes'\n[[action]]\nid='conflict'\nkey='h'\ntitle='Wrong hide'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := envConfig{actionsPath: path}
	linux := hintTextForPlatform(cfg, "linux")
	for _, text := range []string{"Linux Notes", "esc", "cancel"} {
		if !strings.Contains(linux, text) {
			t.Fatalf("missing %q: %s", text, linux)
		}
	}
	if strings.Contains(linux, "Wrong hide") || strings.Contains(linux, "hide overlay") {
		t.Fatal(linux)
	}
	want := "Wisp\nn    Linux Notes\nh    Wrong hide\nesc  cancel"
	if got := hintTextForPlatform(cfg, "darwin"); got != want {
		t.Fatalf("mac format changed: %q", got)
	}
	for _, platform := range []string{"linux", "darwin"} {
		got := strings.ToLower(hintTextForPlatform(cfg, platform))
		if strings.Contains(got, "search") || strings.Contains(got, "space") {
			t.Fatalf("%s advertises reserved Space/search: %q", platform, got)
		}
	}
	cfg.actionsPath = "missing"
	if got := hintTextForPlatform(cfg, "linux"); got != "Wisp\nesc  cancel" {
		t.Fatal(got)
	}
}

func TestNativeHintDoesNotDowngrade(t *testing.T) {
	cfg, helper := hintFixture(t, "printf 'unsupported display' >&2; exit 1")
	t.Setenv("WISP_HINT_COMMAND", "")
	t.Setenv("WISP_HINT_HELPER", helper)
	for _, backend := range []string{"", "native"} {
		t.Setenv("WISP_HINT_BACKEND", backend)
		if err := showHint(cfg, "test"); err == nil || !strings.Contains(err.Error(), "unsupported display") {
			t.Fatalf("backend %q: %v", backend, err)
		}
	}
}

func TestHintCloseSerializesWithStartup(t *testing.T) {
	cfg, helper := hintFixture(t, "printf started > \"$WISP_TEST_STARTED\"; sleep 0.1; printf 'READY\\n' >&3; exec sleep 30")
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("WISP_TEST_STARTED", marker)
	t.Setenv("WISP_HINT_COMMAND", "")
	t.Setenv("WISP_HINT_BACKEND", "native")
	t.Setenv("WISP_HINT_HELPER", helper)
	done := make(chan error, 1)
	go func() { done <- showHint(cfg, "test") }()
	defer closeHint(cfg)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := closeHint(cfg); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.hintPIDPath); !os.IsNotExist(err) {
		t.Fatalf("close missed starting helper: %v", err)
	}
}

func TestDoctorHintOnlyChecksDependencies(t *testing.T) {
	cfg, helper := hintFixture(t, "[ \"$1\" = --check ] || exit 3; printf checked > \"$WISP_TEST_CHECKED\"")
	marker := filepath.Join(t.TempDir(), "checked")
	t.Setenv("WISP_TEST_CHECKED", marker)
	t.Setenv("WISP_HINT_HELPER", helper)
	doctorHint(cfg)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
}
