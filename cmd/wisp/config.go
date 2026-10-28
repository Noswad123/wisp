package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type envConfig struct {
	command           string
	shell             string
	backend           string
	hyprWorkspace     string
	configHome        string
	actionsPath       string
	cacheHome         string
	logPath           string
	socketPath        string
	hintPIDPath       string
	self              string
	kittyInitialWidth string
	kittyInitialHt    string
}

func loadEnv() envConfig {
	home := homeDir()
	configHome := envDefault("WISP_CONFIG_HOME", filepath.Join(envDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "wisp"))
	cacheHome := envDefault("WISP_CACHE_HOME", filepath.Join(envDefault("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "wisp"))
	self, err := os.Executable()
	if err != nil || self == "" {
		self = os.Args[0]
	}

	return envConfig{
		command:           filepath.Base(os.Args[0]),
		shell:             envDefault("WISP_SHELL", envDefault("SHELL", "/bin/zsh")),
		backend:           envDefault("WISP_BACKEND", "auto"),
		hyprWorkspace:     envDefault("WISP_HYPRLAND_WORKSPACE", "wisp"),
		configHome:        configHome,
		actionsPath:       defaultActionsPath(runtime.GOOS, configHome),
		cacheHome:         cacheHome,
		logPath:           envDefault("WISP_LOG_PATH", filepath.Join(cacheHome, "wisp.log")),
		socketPath:        envDefault("WISP_SOCKET_PATH", filepath.Join(runtimeDir(), "wispd.sock")),
		hintPIDPath:       envDefault("WISP_HINT_PID_PATH", filepath.Join(runtimeDir(), "wisp-hint.pid")),
		self:              envDefault("WISP_SELF", self),
		kittyInitialWidth: envDefault("WISP_KITTY_WIDTH", "70c"),
		kittyInitialHt:    envDefault("WISP_KITTY_HEIGHT", "38c"),
	}
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func expand(value string) string {
	if strings.HasPrefix(value, "~/") {
		value = filepath.Join(homeDir(), strings.TrimPrefix(value, "~/"))
	}
	return os.ExpandEnv(value)
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return os.Getenv("HOME")
}

func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("wisp-%d", os.Getuid()))
}

func defaultActionsPath(platform, home string) string {
	if override := os.Getenv("WISP_ACTIONS_PATH"); override != "" {
		return override
	}
	if platform == "linux" {
		path := filepath.Join(home, "actions.linux.toml")
		if st, err := os.Stat(expand(path)); err == nil && !st.IsDir() {
			return path
		}
	}
	return filepath.Join(home, "actions.toml")
}
