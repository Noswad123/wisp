# wisp

![Wisp](img/wisp.png)

`wisp` opens a command in a floating utility surface.

It is part of the Jamal Arcana ecosystem, but it is intentionally generic: `wisp` presents whatever command it receives and does not know about any specific companion tool. Window-manager integration should key off Wisp's stable surface identity instead of guessing that an arbitrary terminal window is special.

## Install

```bash
./install.sh
```

The installer builds the Go CLI from `./cmd/wisp` and installs the resulting
binary.

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

## Usage

```bash
wisp --terminal
wisp nvim README.md
wisp waystone nvim

# Explicit command form.
wisp run --id edit -- nvim README.md

# Named singleton surface. If supported by the active backend, focusing an
# existing wisp:scratch window is preferred over creating a duplicate.
wisp summon scratch -- nvim ~/Projects/darkness/introspection/scratch.md

# Action catalog and palette.
wisp actions init
wisp actions list
wisp action scratch
wisp palette

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
wisp hint show
wisp hint close
```

Legacy `wisp <command> [args...]` and `wisp --terminal` remain supported.

## Actions and palette

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

Actions may set `layout = "floating"` or `layout = "fullscreen"`. This is a
backend hint; Aerospace applies it from inside the launched Kitty surface, and
Hyprland maps fullscreen actions to a fullscreen one-shot rule.

Run actions directly:

```bash
wisp action scratch
```

Open the searchable action palette:

```bash
wisp palette
```

`wisp palette` launches a Wisp surface running `fzf`; selecting an action runs
`wisp action <id>`. A window manager can bind a global key to `wisp palette`,
making Wisp the command palette while the window manager only handles key capture.
When `wispd` is available, client commands auto-start it and delegate action
execution over a local Unix socket. Daemon output is appended to
`~/.cache/wisp/wisp.log` by default.

## Daemon and prefix bindings

Wisp uses a split architecture for modal hotkeys:

```text
window manager/compositor captures Option+Space or Alt+Space
  -> wisp client opens palette or sends an action request
  -> wispd daemon executes/focuses/summons the surface
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

For macOS/Aerospace this emits an `alt-space` Wisp mode and a hint. For
Linux/Hyprland this emits an `ALT+SPACE` submap and a hint. In both cases,
action keys run `wisp action <id>`; `Space` still opens the palette, but the
hint only shows direct keyed actions to avoid duplicating the palette contents.

On macOS, `./install.sh` also installs `wisp-hint-macos` when `swiftc` is
available. `wisp hint show` uses a native non-activating Hexware floater before
falling back to notifications. The floater stays up until `wisp hint close`
runs, which generated prefix bindings do on action selection or `Esc`. Control
the floater with:

| Variable | Purpose |
| --- | --- |
| `WISP_HINT_POSITION` | `top-center` default, `center`, `top-left`, `top-right`, `bottom-left`, `bottom-center`, `bottom-right` |
| `WISP_HINT_DURATION` | Optional seconds before auto-dismiss; default `0` means stay open until closed |
| `WISP_HINT_WIDTH` | Floater width; default `760` |
| `WISP_HINT_FONT_SIZE` | Monospace font size; default `15` |
| `WISP_HINT_BACKEND` | `stdout` or `notification` to force a fallback backend |
| `WISP_HINT_COMMAND` | Custom command that receives hint text on stdin |
| `WISP_HINT_HELPER` | Override path to the macOS hint helper |

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
Hyprland backend automatically. No static Hyprland rule is required; Wisp launches
Kitty with one-shot rules that place the surface on a floating special workspace:

```text
special:wisp
```

Override the special workspace name with:

```bash
WISP_HYPRLAND_WORKSPACE=scratch wisp summon scratch -- nvim ~/scratch.md
```

Named `summon` surfaces try to focus an existing matching `wisp:<id>:` window
before creating a new one. The Hyprland lookup parses `hyprctl -j clients`.

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
| `WISP_LAYOUT` | Surface layout hint: `floating` or `fullscreen` |
| `WISP_SHELL` | Shell used by `wisp --terminal`; defaults to `$SHELL`, then `/bin/zsh` |
| `WISP_BACKEND` | Override backend: `auto`, `aerospace`, `hyprland`, or `kitty` |
| `WISP_HYPRLAND_WORKSPACE` | Hyprland special workspace name; defaults to `wisp` |
| `WISP_ACTIONS_PATH` | Action catalog path; defaults to `~/.config/wisp/actions.toml` |
| `WISP_LOG_PATH` | Log path for palette actions; defaults to `~/.cache/wisp/wisp.log` |
| `WISP_SOCKET_PATH` | Unix socket for `wispd`; defaults to `$XDG_RUNTIME_DIR/wispd.sock` or `/tmp/wisp-$UID/wispd.sock` |
| `WISP_NO_DAEMON` | Run client commands directly without contacting or autostarting `wispd` |
| `WISP_DAEMON_AUTOSTART` | Set to `0` to prevent client commands from starting `wispd` |
| `WISP_HINT_POSITION` | Position for the macOS hint floater; defaults to `top-center` |
| `WISP_HINT_DURATION` | Optional hint floater duration in seconds; defaults to `0`/sticky |

## Dependencies

- macOS `open`
- kitty
- optional: fzf, for `wisp palette`
- optional: Aerospace, for rule-based floating and named-surface focusing
- optional: Hyprland + `hyprctl`, for Linux special-workspace surfaces
- optional: Swift toolchain on macOS, for building the native hint floater
- Go, for building from source

## License

MIT © Jamal Dawson
