package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func hintCommand(cfg envConfig, args []string) error {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	if runtime.GOOS == "linux" && len(args) > 1 {
		session, err := parseHintSession(sub, args[1:])
		if err != nil {
			return err
		}
		if sub == "show" {
			return showHintForSession(cfg, hintText(cfg), session)
		}
		return closeHintForSession(cfg, session.Token)
	}
	switch sub {
	case "show":
		return hint(cfg)
	case "close":
		return closeHint(cfg)
	case "text":
		fmt.Println(hintText(cfg))
		return nil
	default:
		return fmt.Errorf("unknown hint command: %s", sub)
	}
}

func hint(cfg envConfig) error {
	text := hintText(cfg)
	if err := showHint(cfg, text); err != nil {
		fmt.Println(text)
		return err
	}
	return nil
}

func hintMessage(cfg envConfig) string {
	return strings.ReplaceAll(hintText(cfg), "\n", " · ")
}

func hintText(cfg envConfig) string {
	return hintTextForPlatform(cfg, runtime.GOOS)
}

func hintTextForPlatform(cfg envConfig, platform string) string {
	actions, err := loadActions(cfg.actionsPath)
	if err != nil && platform != "linux" {
		return "Wisp\nesc  cancel"
	}
	rows := []struct {
		key   string
		label string
	}{}
	for _, a := range actions {
		key := strings.TrimSpace(a.Key)
		if key == "" || a.ID == "" || strings.EqualFold(key, "space") {
			continue
		}
		if platform == "linux" && (strings.EqualFold(key, "h") || strings.EqualFold(key, "esc")) {
			continue
		}
		label := a.Title
		if label == "" {
			label = a.ID
		}
		rows = append(rows, struct {
			key   string
			label string
		}{key: key, label: label})
	}
	rows = append(rows, struct {
		key   string
		label string
	}{key: "esc", label: "cancel"})

	maxKey := 0
	for _, row := range rows {
		if len(row.key) > maxKey {
			maxKey = len(row.key)
		}
	}
	var b strings.Builder
	b.WriteString("Wisp\n")
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%-*s  %s", maxKey, row.key, row.label)
	}
	return b.String()
}

func showHint(cfg envConfig, text string) error {
	return showHintForSession(cfg, text, nil)
}

func showHintLocked(cfg envConfig, text string) error {
	return showHintLockedForSession(cfg, text, nil)
}

func showHintLockedForSession(cfg envConfig, text string, session *hintSession) error {
	if err := closeHintLocked(cfg); err != nil && runtime.GOOS == "linux" {
		return err
	}
	switch os.Getenv("WISP_HINT_BACKEND") {
	case "stdout":
		fmt.Println(text)
		return nil
	case "notification":
		return notify(cfg, hintMessage(cfg))
	}
	if command := os.Getenv("WISP_HINT_COMMAND"); command != "" {
		cmd := exec.Command("/bin/sh", "-lc", command)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Start(); err != nil {
			return err
		}
		return trackLegacyHintForSession(cfg, cmd, session)
	}
	if runtime.GOOS == "linux" {
		helper, err := findHintHelper(cfg)
		if err == nil {
			return startLinuxHintForSession(cfg, helper, text, 5*time.Second, session)
		}
		if os.Getenv("WISP_HINT_BACKEND") == "native" || os.Getenv("WISP_HINT_HELPER") != "" {
			return err
		}
	}
	if runtime.GOOS == "darwin" {
		if helper, err := findHintHelper(cfg); err == nil {
			cmd := exec.Command(helper)
			cmd.Env = append(os.Environ(), "WISP_HINT_TEXT="+text)
			if err := cmd.Start(); err != nil {
				return err
			}
			return trackLegacyHint(cfg, cmd)
		}
	}
	if hyprlandAvailable() {
		_, err := hyprctlOutput("notify", "1", "1800", "rgb(7aa2f7)", hintMessage(cfg))
		return err
	}
	return notify(cfg, hintMessage(cfg))
}

func writeHintPID(cfg envConfig, pid int) error {
	if err := os.MkdirAll(filepath.Dir(expand(cfg.hintPIDPath)), 0o700); err != nil {
		return err
	}
	return os.WriteFile(expand(cfg.hintPIDPath), []byte(strconv.Itoa(pid)), 0o644)
}

func closeHint(cfg envConfig) error {
	return withHintLock(cfg, func() error { return closeHintLocked(cfg) })
}

func closeHintLocked(cfg envConfig) error {
	data, err := os.ReadFile(expand(cfg.hintPIDPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	_ = os.Remove(expand(cfg.hintPIDPath))
	if runtime.GOOS == "linux" {
		return closeLinuxHint(data)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	_ = proc.Kill()
	return nil
}

func findHintHelper(cfg envConfig) (string, error) {
	return findHintHelperForPlatform(cfg, runtime.GOOS)
}

func findHintHelperForPlatform(cfg envConfig, platform string) (string, error) {
	if helper := os.Getenv("WISP_HINT_HELPER"); helper != "" {
		return findExecutable(helper)
	}
	selfDir := filepath.Dir(cfg.self)
	name := "wisp-hint-macos"
	if platform == "linux" {
		name = "wisp-hint-linux"
	}
	for _, candidate := range []string{
		filepath.Join(selfDir, name),
		name,
	} {
		if path, err := findExecutable(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s was not found beside wisp or on PATH", name)
}
