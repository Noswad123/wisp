package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type action struct {
	ID           string   `toml:"id"`
	Title        string   `toml:"title"`
	Kind         string   `toml:"kind"`
	Key          string   `toml:"key"`
	Layout       string   `toml:"layout"`
	Command      []string `toml:"command"`
	TerminalRows *int     `toml:"terminal_rows"`
}

type actionsFile struct {
	Actions []action `toml:"action"`
}

func launchActionDirect(cfg envConfig, id string) error {
	inv, err := invocationFromAction(cfg, id)
	if err != nil {
		return err
	}
	return launchInvocation(cfg, inv)
}

func actionsCommand(cfg envConfig, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		actions, err := loadActions(cfg.actionsPath)
		if err != nil {
			return err
		}
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
			fmt.Printf("%s\t%s\t%s\t%s\n", a.ID, title, kind, a.Key)
		}
		return nil
	case "init":
		if _, err := os.Stat(expand(cfg.actionsPath)); err == nil {
			return fmt.Errorf("actions file already exists: %s", cfg.actionsPath)
		}
		if err := os.MkdirAll(filepath.Dir(expand(cfg.actionsPath)), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(expand(cfg.actionsPath), []byte(sampleActions()), 0o644); err != nil {
			return err
		}
		fmt.Printf("created %s\n", cfg.actionsPath)
		return nil
	case "path":
		fmt.Println(cfg.actionsPath)
		return nil
	default:
		return fmt.Errorf("unknown actions command: %s", sub)
	}
}

func invocationFromAction(cfg envConfig, id string) (invocation, error) {
	actions, err := loadActions(cfg.actionsPath)
	if err != nil {
		return invocation{}, err
	}
	for _, a := range actions {
		if a.ID != id {
			continue
		}
		kind := a.Kind
		if kind == "" {
			kind = "summon"
		}
		title := a.Title
		if title == "" {
			title = a.ID
		}
		inv := invocation{Mode: kind, ID: a.ID, Title: title, Layout: a.Layout}
		if a.TerminalRows != nil {
			if *a.TerminalRows <= 0 {
				return inv, fmt.Errorf("action %q: terminal_rows must be a positive integer", id)
			}
			inv.TerminalRows = *a.TerminalRows
		}
		if err := validateTerminalRows(inv); err != nil {
			return inv, err
		}
		switch kind {
		case "shell":
			inv.TerminalMode = true
			inv.Command = cfg.shell
			return inv, nil
		case "summon", "run", "exec":
			// handled below
		default:
			return inv, fmt.Errorf("action %q has unsupported kind %q", id, kind)
		}
		if len(a.Command) == 0 {
			return inv, fmt.Errorf("action %q has no command", id)
		}
		inv.Command = expand(a.Command[0])
		for _, arg := range a.Command[1:] {
			inv.Args = append(inv.Args, expand(arg))
		}
		return inv, nil
	}
	return invocation{}, fmt.Errorf("action not found: %s", id)
}

func loadActions(path string) ([]action, error) {
	var parsed actionsFile
	if _, err := toml.DecodeFile(expand(path), &parsed); err != nil {
		return nil, err
	}
	return parsed.Actions, nil
}

func sampleActions() string {
	return `[[action]]
id = "scratch"
title = "Scratch Notes"
kind = "summon"
key = "s"
command = ["nvim", "~/Projects/darkness/introspection/scratch.md"]

[[action]]
id = "aero"
title = "Aerospace Manager"
kind = "summon"
key = "a"
command = ["~/.local/lib/aeros"]

[[action]]
id = "terminal"
title = "Floating Terminal"
kind = "shell"
key = "t"
command = []

[[action]]
id = "waystone"
title = "Waystone"
kind = "summon"
key = "w"
command = ["waystone"]

[[action]]
id = "wyrm"
title = "Wyrm"
kind = "summon"
key = "r"
layout = "fullscreen"
command = ["wyrm"]
`
}
