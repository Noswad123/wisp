package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func findExecutable(command string) (string, error) {
	if filepath.IsAbs(command) || strings.Contains(command, string(os.PathSeparator)) {
		if st, err := os.Stat(command); err == nil && !st.IsDir() {
			return command, nil
		}
	}
	return exec.LookPath(command)
}

func logLine(cfg envConfig, format string, args ...any) {
	_ = os.MkdirAll(filepath.Dir(expand(cfg.logPath)), 0o755)
	f, err := os.OpenFile(expand(cfg.logPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

func notify(cfg envConfig, message string) error {
	if _, err := exec.LookPath("osascript"); err == nil {
		return exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", message, cfg.command)).Run()
	}
	if _, err := exec.LookPath("notify-send"); err == nil {
		return exec.Command("notify-send", "Wisp mode", message).Run()
	}
	return errors.New("no notification command found")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
