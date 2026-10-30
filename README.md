# wisp

![Wisp](img/wisp.png)

## Overview

`wisp` opens a command in a floating utility surface.

It is part of the Jamal Arcana ecosystem, but it is intentionally generic: `wisp` presents whatever command it receives and does not know about any specific companion tool. Window-manager integration should key off Wisp's stable surface identity instead of guessing that an arbitrary terminal window is special.

## Install

```bash
./install.sh
```

The installer builds the Go CLI from `./cmd/wisp` and installs the resulting
binary with the platform hint helper: Swift/Cocoa on macOS (when `swiftc` is
available), or Python/GTK layer-shell on Linux. `make build` also puts the helper
beside `bin/wisp` for testing without installation.

By default this installs to `~/.local/bin`. Override with:

```bash
WISP_BIN_DIR=/usr/local/bin ./install.sh
```

Completions install by default to:

```text
~/.local/share/zsh/site-functions/_wisp
~/.local/share/bash-completion/completions/wisp
```

Override or disable completion installation with:

```bash
WISP_ZSH_COMPLETION_DIR=/path/to/site-functions ./install.sh
WISP_BASH_COMPLETION_DIR=/path/to/bash-completion ./install.sh
WISP_INSTALL_COMPLETIONS=0 ./install.sh
```

## Example usage

```bash
wisp --terminal
wisp nvim README.md
wisp waystone nvim

# Explicit command form.
wisp run --id edit -- nvim README.md

# Named singleton surface. If supported by the active backend, focusing an
# existing wisp:scratch window is preferred over creating a duplicate.
wisp summon scratch -- nvim ~/Projects/darkness/introspection/scratch.md

# Action catalog, native Palette, and secondary CLI menu.
wisp actions init
wisp actions list
wisp action scratch
wisp palette
wisp menu # Explicit Kitty/fzf interface, not the native Palette
wisp show  # Hyprland: ensure the utility overlay is visible
wisp hide  # Hyprland: hide the overlay without closing its applications

# Daemon-backed action/session control.
wisp daemon start
wisp daemon status
wispd status

# Diagnostics and window-manager rule snippets.
wisp doctor
wisp rules aerospace
wisp rules hyprland
wisp bindings aerospace
wisp bindings hyprland
wisp palette show
wisp palette close
wisp palette text
```

Legacy `wisp <command> [args...]` and `wisp --terminal` remain supported.

### Hyprland compatibility

Runtime surface launches (including `wisp menu`) and summon focus/toggle operations support
both legacy Hyprlang and Lua configuration providers. Wisp detects the provider
using read-only `hyprctl -j systeminfo` / `hyprctl systeminfo` queries; it does not
probe by dispatching commands. Older versions without a provider field retain
legacy dispatch syntax. Lua launches use `hl.dsp.exec_cmd` with a rule table;
shell command arguments and Lua strings are quoted separately.

`wisp rules hyprland` and `wisp bindings hyprland` still generate **legacy
hyprland.conf syntax only**, not Lua configuration. Lua config/rule generation is
deferred; do not paste those snippets into `hyprland.lua`. Runtime support does
not require generated snippets.

## Actions and Palette

Wisp can also act as a small Raycast/Tuna-like launcher. Actions live in:

```text
~/.config/wisp/actions.toml
```

Create a starter catalog with:

```bash
wisp actions init
```

Example action:

```toml
[[action]]
id = "scratch"
title = "Scratch Notes"
kind = "summon"
key = "s"
layout = "floating"
command = ["nvim", "~/Projects/darkness/introspection/scratch.md"]
```

Supported `kind` values:

| Kind | Behavior |
| --- | --- |
| `summon` | Named singleton surface; focus existing if possible, otherwise launch |
| `run` | Launch a new Wisp surface |
| `shell` | Launch a floating shell |
| `exec` | Start the action command directly without wrapping it in a Wisp terminal surface |

Actions may set `layout = "floating"` or `layout = "fullscreen"`. This is a
backend hint for surfaced actions; Aerospace applies it from inside the launched
Kitty surface, and Hyprland maps fullscreen actions to a fullscreen one-shot
rule. Use `kind = "exec"` for GUI/menu handoff actions that should be selected
from the palette but should not create their own Wisp terminal.

### Per-action initial terminal height

Floating terminal actions (`summon`, `run`, or `shell`) may opt into a Kitty
cell-based initial height:

```toml
terminal_rows = 28
```

Add this key inside only the desired `[[action]]` table. It must be a positive
integer; explicit zero/negative values, `kind = "exec"`, and fullscreen layout
are rejected when launching the action. Omission retains existing behavior.
No application names are special-cased, and no catalogs are changed by this option.

Kitty receives `initial_window_height=28c`, overriding `WISP_KITTY_HEIGHT` for
that action only, with remembered-size restoration disabled. On Hyprland, Wisp
omits the conflicting 900×600 spawn size rule for opted-in actions. After clearing
tile/fullscreen state it observes the normal mapped client height and uses that
height, with the usual preferred 900 logical-pixel width, fitted to the monitor.
Adopted tiled/fullscreen windows use their observed normal floating height too.
Valid user-resized floats retain their geometry on later summons; this is not a
periodic minimum-size policy.

This is an **initial preference, not a guaranteed terminal row count**: monitor
constraints can limit it, and compositor pixels do not measure TTY rows. A
provisional mapped size or later client/compositor requests may affect the actual
terminal dimensions; verify rows in the live terminal if an application requires
a minimum. Wisp does not use Kitty remote control or enforce a row/column minimum.
Other Hyprland actions retain 900×600. macOS retains its default Kitty 70c×38c
(or environment overrides); an explicit `terminal_rows` changes only that action's
initial Kitty height there as well.

Run actions directly:

```bash
wisp action scratch
```

### API / Interfaces

Show the native passive prefix **Palette**:

```bash
wisp palette
```

`wisp palette [show|close|text]` uses the native helper (`show` by default);
`text` prints its labels. It never daemon-delegates, captures keys, or enters a
compositor mode by itself. `wisp hint` remains a compatibility alias.

`wisp menu` is the explicit secondary searchable Kitty/fzf CLI interface.
Both interfaces use the same action catalog and canonical action resolver;
the compositor owns native Palette key capture. CLI Menu selection calls
`launchActionDirect(cfg, id)` locally against the catalog the picker displayed,
even when a daemon launched the Menu surface. It does not delegate the selected
ID to a potentially different daemon catalog. Standalone `wisp action <id>`
delegation policy is unchanged, as are overlay locks, failure recovery and exec
supervision. The menu client delegates the **surface launch** using the existing
request, understood by older daemons:

```text
run --id palette --title 'Wisp Menu' --path '' --dir '' --layout '' -- /usr/bin/env -- <client context assignments> <client self> _palette
```

This is an argv schematic, not a command to paste. The launch command is
`/usr/bin/env`, whose first argument is `--` to avoid target-path inference.
`paletteContextEnv` supplies the **client's resolved** `WISP_ACTIONS_PATH`,
`WISP_SOCKET_PATH`, `WISP_LOG_PATH`, `WISP_SELF`, `WISP_CONFIG_HOME`,
`WISP_CACHE_HOME`, `WISP_HINT_PID_PATH`, `WISP_SHELL`, `WISP_BACKEND`,
`WISP_HYPRLAND_WORKSPACE`, `WISP_KITTY_WIDTH` and `WISP_KITTY_HEIGHT`.
It also supplies caller values for `WISP_NO_DAEMON`, `WISP_DAEMON_BYPASS`,
`WISP_DAEMON_AUTOSTART`, `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE`, including
explicit empty assignments when unset. The final command is the client executable
plus `_palette`, not the daemon's executable. This preserves caller context with
older or different-path daemons without broadening the strict Hyprland
ordinary-child context guard. The earlier direct-client-self-only delegation
payload is superseded by this wrapper.

No new protocol or daemon restart is needed just for this rename. Internal
`_palette`, surface ID `palette`, mode `run`, `WISP_HINT_*`, helper names and
PID/overlay state are intentionally unchanged. Eligible action/surface commands
auto-start and delegate to `wispd` over a local Unix socket; daemon output defaults
to `~/.cache/wisp/wisp.log`.

On Linux, an existing `actions.linux.toml` in Wisp's config directory takes
precedence over `actions.toml`. `WISP_ACTIONS_PATH` overrides both. This lets Linux
omit Aerospace-only actions and select floating defaults without changing macOS.
Restart an existing daemon after changing its startup catalog/environment.

Actions with `kind = "exec"` launch directly without Kitty, suitable for native
menus and applications. A detached supervisor records their output and exit errors
in the Wisp log; a successful launch acknowledges process start, not application
completion. Terminal `run`/`shell`/`summon` actions use utility surfaces instead.

## Data Flow

### Daemon and prefix bindings

Wisp uses a split architecture for modal hotkeys:

```text
window manager/compositor captures Option+Space or Alt+Space
  -> compositor shows native Palette AND enters its Wisp mode/submap
  -> action key closes Palette, dispatches wisp action <id>, exits mode
  -> wispd executes the action (exec launches directly; others use surfaces)
```

Start or inspect the daemon with:

```bash
wisp daemon start
wisp daemon status
wisp daemon stop

# If installed through ./install.sh, wispd is also available as a symlink.
wispd status
```

Client launches auto-start `wispd` by default. Disable daemon delegation with:

```bash
WISP_NO_DAEMON=1 wisp action scratch
```

Actions can define a `key` used by binding generators. Generate snippets with:

```bash
wisp bindings aerospace
wisp bindings hyprland
```

For macOS/Aerospace this emits an `alt-space` Wisp mode and Palette show/close.
For Linux/Hyprland this emits an `ALT+SPACE` submap and Palette show/close. In both cases,
action keys run `wisp action <id>`. Search is not a prefix-menu option because it
duplicates the direct actions. Space is consumed without leaving the mode or
launching the CLI menu; Escape closes Palette and cancels the mode. Binding only
`wisp palette` is insufficient: the compositor must also enter mode.

On macOS, `./install.sh` also installs `wisp-hint-macos` when `swiftc` is
available. `wisp palette show` uses a native non-activating Hexware floater before
falling back to notifications. The floater stays up until `wisp palette close`
runs, which generated prefix bindings do on action selection or `Esc`.

On Linux, `wisp-hint-linux` provides the equivalent passive native Wayland panel.
It runs in the overlay layer, reserves no screen space, and captures neither
keyboard nor pointer input. The compositor owns the prefix/action/Escape keys;
the helper only renders the selected catalog's labels. It is independent of the
utility workspace. The CLI `wisp menu` still uses Kitty/fzf on both systems, but is
not part of the native Palette. Palette labels show keyed catalog actions and `Esc`
(cancel), without duplicate Search or Hide controls. The explicit `wisp hide`
CLI command remains available for overlay management.

The Linux helper uses **system `/usr/bin/python3`**, PyGObject, Pycairo, GTK 3 and
gtk-layer-shell. On Arch-family hosts:

```sh
sudo pacman -S --needed python-gobject python-cairo gtk3 gtk-layer-shell
wisp doctor
```

### Edge Cases

`wisp doctor` checks helper imports without opening a window. Native show waits
for the helper's mapped-readiness signal and reports startup failures rather than
silently substituting a notification. Prefix integrations should use session
tokens to prevent delayed show/close operations from affecting a newer prefix:

```sh
wisp palette show --session TOKEN --state /absolute/runtime/state-file
wisp palette close --session TOKEN
```

Publish `TOKEN` plus a newline atomically before show, and invalidate it
synchronously on action/cancel. The dotfiles Lua integration implements this and
generation-guards asynchronous failure recovery. Standalone show/close requires
no session flags. The helper remains one foreground process; custom commands
should use `exec` so close owns the actual renderer.

| Variable | Purpose |
| --- | --- |
| `WISP_HINT_POSITION` | `top-center` default, `center`, `top-left`, `top-right`, `bottom-left`, `bottom-center`, `bottom-right` |
| `WISP_HINT_DURATION` | Optional seconds before auto-dismiss; default `0` means stay open until closed |
| `WISP_HINT_WIDTH` | Floater width; default `760` |
| `WISP_HINT_FONT_SIZE` | Monospace font size; default `15` |
| `WISP_HINT_BACKEND` | `native` requires the native helper; `stdout` or `notification` forces an alternate backend |
| `WISP_HINT_COMMAND` | Custom command that receives hint text on stdin |
| `WISP_HINT_HELPER` | Override path to the platform hint helper |

## Surface identity

Wisp titles are normalized to:

```text
wisp:<id>: <label>
```

Examples:

```text
wisp:nvim: README.md
wisp:scratch: ~/Projects/darkness/introspection/scratch.md
```

The launched command receives:

| Variable | Purpose |
| --- | --- |
| `WISP` | Set to `1` inside Wisp surfaces |
| `WISP_ID` | Stable Wisp surface id |
| `WISP_ROLE` | `run`, `shell`, or `summon` |
| `WISP_TITLE` | Full normalized Wisp title |
| `WISP_LAYOUT` | Surface layout hint: `floating` or `fullscreen` |

## Aerospace

Prefer an Aerospace detection rule over post-launch correction:

```bash
wisp rules aerospace
```

Current output:

```toml
on-window-detected = [
    { if.app-id = 'net.kovidgoyal.kitty', if.window-title-regex-substring = '^wisp:', run = 'layout floating' },
]
```

Wisp still keeps a best-effort post-launch `aerospace layout floating` fallback for compatibility.

Prefix-key bindings can be generated with:

```bash
wisp bindings aerospace
```

## Hyprland

When `HYPRLAND_INSTANCE_SIGNATURE` and `hyprctl` are available, Wisp uses the
Hyprland backend automatically. No static Hyprland rule is required. Wisp owns
floating utility windows inside a dedicated special-workspace overlay:

```text
special:wisp
```

Override the special workspace name with:

```bash
WISP_HYPRLAND_WORKSPACE=scratch wisp summon scratch -- nvim ~/scratch.md
```

Opening/summoning a surface means **ensure visible and raise**, never blind toggle.
Wisp observes mapped clients and monitor state before reporting readiness. It
uses silent workspace placement so a late launch cannot undo an explicit hide.
Closing the CLI menu closes only its surface: its selected terminal action is
already mapped and presented before the menu exits. Launch errors remain
visible with a retry prompt.

Utilities default to floating, centered at a preferred 900×600 logical size fitted
to the monitor. Valid user-resized floating geometry survives later summons;
older tiled/fullscreen utilities are normalized when adopted. Fullscreen requires
an explicit action `layout = "fullscreen"` or CLI `--layout fullscreen`; Linux
does not inherit the parent utility's `WISP_LAYOUT` metadata as an instruction.

Named `summon` surfaces reuse stable Wisp class identity (with older-title
compatibility) rather than duplicate an existing window. Operations serialize
across daemon and direct clients. A readiness timeout leaves a pending guard
because the process may still map later; inspect the reported guard and compositor
state before removing it or retrying a launch. Wisp does not kill uncertain apps.

`wisp hide` hides only the utility overlay; `wisp show` reveals it idempotently.
If already visible on another monitor, Wisp focuses it there. It does not poll
continuously to fight an explicit user hide or unrelated compositor commands.

Prefix-key submap bindings can be generated with:

```bash
wisp bindings hyprland
```

## Environment

| Variable | Purpose |
| --- | --- |
| `WISP_TITLE` | Override floating window title |
| `WISP_ID` | Stable surface id |
| `WISP_DIR` | Override working directory |
| `WISP_PATH` | Path used to infer title and working directory |
| `WISP_LAYOUT` | Surface metadata; Linux layout is selected explicitly by action/CLI, not inherited |
| `WISP_SHELL` | Shell used by `wisp --terminal`; defaults to `$SHELL`, then `/bin/zsh` |
| `WISP_BACKEND` | Override backend: `auto`, `aerospace`, `hyprland`, or `kitty` |
| `WISP_KITTY_WIDTH` | Initial Kitty width for Hyprland/macOS; default `70c` (Hyprland controller normally prefers 900 logical pixels) |
| `WISP_KITTY_HEIGHT` | Initial Kitty height for Hyprland/macOS; default `38c`; per-action `terminal_rows` takes precedence (ordinary Hyprland actions still prefer 600 logical pixels) |
| `WISP_HYPRLAND_WORKSPACE` | Hyprland special workspace name; defaults to `wisp` |
| `WISP_ACTIONS_PATH` | Catalog override; Linux prefers existing `actions.linux.toml`, otherwise `actions.toml` |
| `WISP_LOG_PATH` | Log path for menu/actions; defaults to `~/.cache/wisp/wisp.log` |
| `WISP_SOCKET_PATH` | Unix socket for `wispd`; defaults to `$XDG_RUNTIME_DIR/wispd.sock` or `/tmp/wisp-$UID/wispd.sock` |
| `WISP_NO_DAEMON` | Run client commands directly without contacting or autostarting `wispd` |
| `WISP_DAEMON_AUTOSTART` | Set to `0` to prevent client commands from starting `wispd` |
| `WISP_HINT_POSITION` | Position for the native hint panel; defaults to `top-center` |
| `WISP_HINT_DURATION` | Optional hint floater duration in seconds; defaults to `0`/sticky |

## Dependencies

- macOS `open`
- kitty, for terminal surfaces and `wisp menu` (not native Palette/direct exec)
- optional: fzf, for `wisp menu`
- optional: Aerospace, for rule-based floating and named-surface focusing
- optional: Hyprland + `hyprctl`, for Linux special-workspace surfaces
- optional: Swift toolchain on macOS, for building the native hint floater
- optional: system Python 3, PyGObject/Pycairo, GTK 3 and gtk-layer-shell, for the Linux native Wayland hint panel
- Go, for building from source

## Development

The CLI lives in `cmd/wisp` as a single Go package, organized by responsibility:

| Files | Responsibility |
| --- | --- |
| `main.go` | Entry point, command routing, and daemon delegation policy |
| `config.go` | Environment configuration, runtime paths, and catalog selection |
| `daemon.go` | Daemon lifecycle and Unix-socket request/response handling |
| `invocation.go` | Invocation model, CLI option parsing, and validation |
| `actions.go`, `palette.go` | Action catalogs and the interactive fzf CLI menu (internal name retained) |
| `backend.go`, `backend_aerospace.go`, `backend_hyprland.go` | Launch orchestration and window-manager adapters |
| `overlay.go` | Hyprland overlay state, serialization, and window presentation |
| `exec_action.go` | Detached execution, supervision, and exit logging |
| `hint.go`, `hint_native.go`, `hint_session.go` | Native Palette/compatibility hint commands, helper lifecycle, and session ownership |
| `palette_context.go` | Environment propagation for the Hyprland CLI menu |
| `commands.go`, `process.go` | Help/diagnostic/configuration output and shared process helpers |

Backend implementations stay in the same package and are selected at runtime;
they are not restricted by OS-specific build tags. Build the package, not an
individual source file:

```bash
go build ./cmd/wisp
make fmt                  # Format all CLI Go sources and tests
make check                # Go and headless Python tests
go test -race ./...
go vet ./...
```

### Numen integration test

Opt in with an absolute path to an existing Numen executable (otherwise the test
skips):

```bash
CGO_ENABLED=0 NUMEN_TEST_BINARY="$HOME/.local/bin/numen" go test ./cmd/wisp -run '^TestNumenActionIntegration$' -count=1
```

This runs actual `numen wisp-action` preview and explicit temporary-catalog
installation, checks idempotence, and loads/resolves the result through Wisp's
action pipeline. It verifies `kind = "exec"`, `key = "n"`, exact three-element
quoted/Unicode argv `[absoluteExecutable, "--socket", absoluteSocket]` (no `menu`), and
leading `--socket` preserving cwd without a target path. It does not launch a
palette, terminal or Numen service, or modify the home catalog. See Numen's
[Wisp integration guide](../numen/WISP.md) for opt-in setup, path restrictions and
[UI-to-wire validation boundaries](../numen/WISP.md#wisp-integration-validation).

### Related files and Numen usage

The optional generated Numen action launches bare Numen directly: native Palette
key **n** opens one unattached familiar, reusing/bootstrap-starting its service;
repeat adds views up to 16. Attach to a supported live app/chat through the
familiar's click menu. No Kitty/fzf is needed for this default. Explicit
`numen menu` remains an advanced TTY interface and requires Wisp `run`/`summon`,
not the default `exec` entry. See the canonical [Numen Wisp guide](../numen/WISP.md)
for strict installer migration, local on-disk/reload status, tests and pending
manual acceptance. Command routing is in `cmd/wisp/main.go`, generated bindings
in `commands.go`, native rendering in `hint*.go`, and CLI selection in `palette.go`.

Updated Wisp (native helper/completions included) and debug Numen installation
are confirmed. Final Go tests/vet with CGO disabled, 14 headless Python helper
tests, shell syntax checks and the installed Numen contract test passed. The
installed direct-exec smoke opened two native unattached views in one prestarted
private service without Kitty/fzf; it proves reuse, not offline bootstrap or
physical Palette key delivery. AeroSpace `n` is on disk but **not yet active**:
review unrelated music-workspace edits before explicitly reloading. See the
[canonical evidence and manual requirements](../numen/WISP.md#wisp-integration-validation).

## License

MIT © Jamal Dawson
