package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	// Internal detached exec supervisor must also work when invoked as wispd.
	if len(os.Args) > 1 && os.Args[1] == "_exec_supervise" {
		if err := superviseExec(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "wisp exec supervisor: %v\n", err)
			os.Exit(1)
		}
		return
	}
	cfg := loadEnv()
	if cfg.command == "wispd" {
		if err := daemonCommand(cfg, os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", cfg.command, err)
			os.Exit(1)
		}
		return
	}
	if err := run(cfg, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cfg.command, err)
		os.Exit(1)
	}
}

func run(cfg envConfig, args []string) error {
	if len(args) == 0 {
		usage(cfg, os.Stderr)
		return errors.New("missing command")
	}

	if shouldDelegate(args) && daemonUsable() {
		if err := ensureDaemon(cfg); err == nil {
			return sendDaemon(cfg, daemonRequest{Op: "launch", Args: args})
		}
	}

	return runDirect(cfg, args)
}

func runDirect(cfg envConfig, args []string) error {
	if len(args) == 0 {
		usage(cfg, os.Stderr)
		return errors.New("missing command")
	}

	switch args[0] {
	case "-h", "--help", "help":
		usage(cfg, os.Stdout)
		return nil
	case "daemon":
		return daemonCommand(cfg, args[1:])
	case "doctor":
		return doctor(cfg)
	case "rules":
		backend := "aerospace"
		if len(args) > 1 {
			backend = args[1]
		}
		return rules(backend)
	case "bindings":
		backend := "aerospace"
		if len(args) > 1 {
			backend = args[1]
		}
		return bindings(cfg, backend)
	case "hint":
		return hintCommand(cfg, args[1:])
	case "actions":
		return actionsCommand(cfg, args[1:])
	case "_palette":
		return paletteBody(cfg)
	case "palette":
		inv := invocation{Mode: "run", ID: "palette", Title: "Wisp Palette", Command: cfg.self, Args: []string{"_palette"}}
		return launchInvocation(cfg, inv)
	case "show", "hide":
		return overlayCommand(cfg, args[0] == "show")
	case "action":
		if len(args) < 2 {
			return errors.New("action requires an id")
		}
		return launchActionDirect(cfg, args[1])
	case "_launch_action":
		if len(args) < 2 {
			return errors.New("_launch_action requires an id")
		}
		logLine(cfg, "palette launching action=%s", args[1])
		inv, err := invocationFromAction(cfg, args[1])
		if err != nil {
			return err
		}
		return launchInvocation(cfg, inv)
	}

	inv, err := parseInvocation(cfg, args)
	if err != nil {
		return err
	}
	return launchInvocation(cfg, inv)
}

func shouldDelegate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "show", "hide", "action", "summon", "shell", "run", "palette", "-t", "--terminal":
		return true
	case "daemon", "doctor", "rules", "bindings", "hint", "actions", "_palette", "_launch_action", "-h", "--help", "help":
		return false
	default:
		return true
	}
}
