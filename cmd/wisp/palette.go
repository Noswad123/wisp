package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func paletteBody(cfg envConfig) error {
	for {
		err := paletteAttempt(cfg)
		if err == nil {
			return nil
		}
		logLine(cfg, "palette error: %v", err)
		fmt.Fprintf(os.Stderr, "\nWisp: %v\nPress Enter to retry (Ctrl-C to cancel).\n", err)
		if _, readErr := bufio.NewReader(os.Stdin).ReadString('\n'); readErr != nil {
			return err
		}
	}
}

func paletteAttempt(cfg envConfig) error {
	if _, err := exec.LookPath("fzf"); err != nil {
		return errors.New("fzf is required for wisp palette")
	}

	actions, err := loadActions(cfg.actionsPath)
	if err != nil {
		return err
	}
	var input strings.Builder
	for _, a := range actions {
		if a.ID == "" {
			continue
		}
		title := a.Title
		if title == "" {
			title = a.ID
		}
		kind := a.Kind
		if kind == "" {
			kind = "summon"
		}
		fmt.Fprintf(&input, "%s\t%s\t%s\n", a.ID, title, kind)
	}

	cmd := exec.Command("fzf", "--prompt=Wisp> ", "--delimiter=\t", "--with-nth=2,3", "--height=100%", "--border", "--ansi")
	cmd.Stdin = strings.NewReader(input.String())
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 130) {
			return nil
		}
		return fmt.Errorf("fzf: %w", err)
	}
	selection := strings.TrimSpace(out.String())
	if selection == "" {
		return nil
	}
	actionID := strings.SplitN(selection, "\t", 2)[0]
	logLine(cfg, "palette selected action=%s", actionID)
	logLine(cfg, "palette launching action=%s", actionID)
	if err := run(cfg, []string{"action", actionID}); err != nil {
		return fmt.Errorf("palette action %q: %w", actionID, err)
	}
	return nil
}
