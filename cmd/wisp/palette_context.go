package main

import "os"

// Hyprland's exec inherits the compositor's environment, not the requesting
// Wisp client's. Carry only the resolved Wisp context and explicit palette UI
// options across that boundary. Prefix the internal command, not Kitty or
// ordinary utilities; shellJoin quotes each assignment as a single argument.
func hyprlandChildArgs(cfg envConfig, inv invocation) []string {
	if len(inv.OriginalArgs) != 2 || inv.OriginalArgs[0] != cfg.self || inv.OriginalArgs[1] != "_palette" {
		return inv.OriginalArgs
	}
	args := []string{
		"env",
		"WISP_ACTIONS_PATH=" + cfg.actionsPath,
		"WISP_SOCKET_PATH=" + cfg.socketPath,
		"WISP_LOG_PATH=" + cfg.logPath,
		"WISP_SELF=" + cfg.self,
		"WISP_CONFIG_HOME=" + cfg.configHome,
		"WISP_CACHE_HOME=" + cfg.cacheHome,
		"WISP_HINT_PID_PATH=" + cfg.hintPIDPath,
		"WISP_SHELL=" + cfg.shell,
		"WISP_BACKEND=" + cfg.backend,
		"WISP_HYPRLAND_WORKSPACE=" + cfg.hyprWorkspace,
		"WISP_KITTY_WIDTH=" + cfg.kittyInitialWidth,
		"WISP_KITTY_HEIGHT=" + cfg.kittyInitialHt,
	}
	// Include empty values too: stale compositor settings must not change the
	// caller's delegation policy or silently inject fzf selection options.
	for _, key := range []string{"WISP_NO_DAEMON", "WISP_DAEMON_BYPASS", "WISP_DAEMON_AUTOSTART", "FZF_DEFAULT_OPTS", "FZF_DEFAULT_OPTS_FILE"} {
		args = append(args, key+"="+os.Getenv(key))
	}
	return append(args, inv.OriginalArgs...)
}
