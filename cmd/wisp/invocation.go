package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type invocation struct {
	Mode         string
	TerminalMode bool
	ID           string
	Title        string
	TargetPath   string
	WorkDir      string
	Command      string
	Args         []string
	OriginalArgs []string
	WindowTitle  string
	SurfaceID    string
	Layout       string
	TerminalRows int // Zero means no per-action initial height preference.
}

func parseInvocation(cfg envConfig, args []string) (invocation, error) {
	inv := invocation{
		Mode:       "run",
		ID:         os.Getenv("WISP_ID"),
		Title:      os.Getenv("WISP_TITLE"),
		TargetPath: os.Getenv("WISP_PATH"),
		WorkDir:    os.Getenv("WISP_DIR"),
		Layout:     os.Getenv("WISP_LAYOUT"),
	}
	// Linux utilities do not inherit their parent's surface layout.
	if runtime.GOOS == "linux" {
		inv.Layout = ""
	}

	switch args[0] {
	case "run":
		var rest []string
		rest = parseOptions(&inv, args[1:])
		if len(rest) == 0 {
			return inv, errors.New("missing command")
		}
		inv.Command, inv.Args = rest[0], rest[1:]
	case "shell":
		inv.Mode = "shell"
		inv.TerminalMode = true
		inv.Command = cfg.shell
		rest := parseOptions(&inv, args[1:])
		if len(rest) > 0 {
			return inv, errors.New("shell does not accept command arguments; use run instead")
		}
	case "summon":
		if len(args) < 2 {
			return inv, errors.New("summon requires an id")
		}
		inv.Mode = "summon"
		inv.ID = args[1]
		rest := parseOptions(&inv, args[2:])
		if len(rest) == 0 {
			return inv, errors.New("missing command")
		}
		inv.Command, inv.Args = rest[0], rest[1:]
	case "-t", "--terminal":
		if len(args) > 1 {
			return inv, errors.New("--terminal does not accept arguments")
		}
		inv.Mode = "shell"
		inv.TerminalMode = true
		inv.Command = cfg.shell
	default:
		inv.Command, inv.Args = args[0], args[1:]
	}

	return inv, nil
}

func parseOptions(inv *invocation, args []string) []string {
	for len(args) > 0 {
		switch args[0] {
		case "--":
			return args[1:]
		case "--id":
			if len(args) >= 2 {
				inv.ID = args[1]
				args = args[2:]
				continue
			}
		case "--title":
			if len(args) >= 2 {
				inv.Title = args[1]
				args = args[2:]
				continue
			}
		case "--dir":
			if len(args) >= 2 {
				inv.WorkDir = args[1]
				args = args[2:]
				continue
			}
		case "--path":
			if len(args) >= 2 {
				inv.TargetPath = args[1]
				args = args[2:]
				continue
			}
		case "--layout":
			if len(args) >= 2 {
				inv.Layout = args[1]
				args = args[2:]
				continue
			}
		}
		return args
	}
	return nil
}

func resolveInvocation(cfg envConfig, inv *invocation) error {
	if err := validateTerminalRows(*inv); err != nil {
		return err
	}
	if inv.Command == "" {
		return errors.New("missing command")
	}

	if _, err := findExecutable(inv.Command); err != nil {
		notify(cfg, fmt.Sprintf("%s was not found on PATH", inv.Command))
		return fmt.Errorf("command not found: %s", inv.Command)
	}

	if inv.TerminalMode {
		inv.OriginalArgs = []string{inv.Command}
	} else {
		inv.OriginalArgs = append([]string{inv.Command}, inv.Args...)
	}

	if inv.Mode != "exec" && inv.TargetPath == "" && len(inv.Args) > 0 && !strings.HasPrefix(inv.Args[0], "-") {
		inv.TargetPath = inv.Args[0]
	}

	defaultWorkDir := ""
	displayPath := ""
	if inv.TargetPath != "" {
		target := expand(inv.TargetPath)
		if st, err := os.Stat(target); err == nil && st.IsDir() {
			defaultWorkDir = target
			displayPath = target
		} else {
			defaultWorkDir = filepath.Dir(target)
			displayPath = target
		}
	} else {
		cwd, _ := os.Getwd()
		defaultWorkDir = cwd
	}

	if inv.WorkDir == "" {
		inv.WorkDir = defaultWorkDir
	} else {
		inv.WorkDir = expand(inv.WorkDir)
	}
	if st, err := os.Stat(inv.WorkDir); err != nil || !st.IsDir() {
		return fmt.Errorf("working directory does not exist: %s", inv.WorkDir)
	}

	inv.Layout = strings.ToLower(strings.TrimSpace(inv.Layout))
	if inv.Layout == "" {
		inv.Layout = "floating"
	}
	switch inv.Layout {
	case "floating", "fullscreen":
	default:
		return fmt.Errorf("unsupported layout: %s", inv.Layout)
	}

	label := inv.Command
	if displayPath != "" {
		label = displayPathForUser(displayPath)
	} else if inv.TerminalMode {
		label = "terminal"
	}

	if inv.ID == "" {
		if inv.Title != "" {
			inv.SurfaceID = slugify(inv.Title)
		} else if inv.TerminalMode {
			inv.SurfaceID = "terminal"
		} else {
			inv.SurfaceID = slugify(inv.Command)
		}
	} else {
		inv.SurfaceID = slugify(inv.ID)
	}

	if inv.Title == "" {
		inv.WindowTitle = label
	} else {
		inv.WindowTitle = inv.Title
	}
	if !strings.HasPrefix(inv.WindowTitle, "wisp:") {
		inv.WindowTitle = fmt.Sprintf("wisp:%s: %s", inv.SurfaceID, inv.WindowTitle)
	}
	return nil
}

func validateTerminalRows(inv invocation) error {
	if inv.TerminalRows < 0 {
		return fmt.Errorf("action %q: terminal_rows must be a positive integer", inv.ID)
	}
	if inv.TerminalRows == 0 {
		return nil
	}
	if inv.Mode != "run" && inv.Mode != "shell" && inv.Mode != "summon" {
		return fmt.Errorf("action %q: terminal_rows is only supported for terminal run/shell/summon actions, not %q", inv.ID, inv.Mode)
	}
	if layout := strings.ToLower(strings.TrimSpace(inv.Layout)); layout != "" && layout != "floating" {
		return fmt.Errorf("action %q: terminal_rows requires floating layout (not %q)", inv.ID, inv.Layout)
	}
	return nil
}

func displayPathForUser(path string) string {
	home := homeDir()
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~/" + strings.TrimPrefix(path, home+string(os.PathSeparator))
	}
	return path
}

func slugify(value string) string {
	value = filepath.Base(value)
	value = strings.ToLower(value)
	re := regexp.MustCompile(`[^a-z0-9_.-]+`)
	value = strings.Trim(re.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "surface"
	}
	return value
}
