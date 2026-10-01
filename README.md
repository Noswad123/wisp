# wisp

`wisp` opens a command in a floating utility surface.

It is part of the Jamal Arcana ecosystem, but it is intentionally generic: `wisp` presents whatever command it receives and does not know about any specific companion tool. Window-manager integration should key off Wisp's stable surface identity instead of guessing that an arbitrary terminal window is special.

## Install

```bash
./install.sh
```

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
wisp yazi ~/Downloads
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

# Diagnostics and window-manager rule snippets.
wisp doctor
wisp rules aerospace
wisp rules hyprland
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
command = ["nvim", "~/Projects/darkness/introspection/scratch.md"]
```

Supported `kind` values:

| Kind | Behavior |
| --- | --- |
| `summon` | Named singleton surface; focus existing if possible, otherwise launch |
| `run` | Launch a new Wisp surface |
| `shell` | Launch a floating shell |

Run actions directly:

```bash
wisp action scratch
```

Open the searchable action palette:

```bash
wisp palette
```

`wisp palette` launches a Wisp surface running `fzf`; selecting an action runs
`wisp action <id>` as a detached process. A window manager can bind a global key to `wisp palette`,
making Wisp the command palette while the window manager only handles key capture.
Detached palette action output is appended to `~/.cache/wisp/wisp.log` by default.

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
before creating a new one. The Hyprland lookup uses `python3` to parse
`hyprctl -j clients`.

## Environment

| Variable | Purpose |
| --- | --- |
| `WISP_TITLE` | Override floating window title |
| `WISP_ID` | Stable surface id |
| `WISP_DIR` | Override working directory |
| `WISP_PATH` | Path used to infer title and working directory |
| `WISP_SHELL` | Shell used by `wisp --terminal`; defaults to `$SHELL`, then `/bin/zsh` |
| `WISP_BACKEND` | Override backend: `auto`, `aerospace`, `hyprland`, or `kitty` |
| `WISP_HYPRLAND_WORKSPACE` | Hyprland special workspace name; defaults to `wisp` |
| `WISP_ACTIONS_PATH` | Action catalog path; defaults to `~/.config/wisp/actions.toml` |
| `WISP_LOG_PATH` | Log path for detached palette actions; defaults to `~/.cache/wisp/wisp.log` |

## Dependencies

- macOS `open`
- kitty
- python3, for action catalog parsing
- optional: fzf, for `wisp palette`
- optional: Aerospace, for rule-based floating and named-surface focusing
- optional: Hyprland + `hyprctl` + `python3`, for Linux special-workspace surfaces

## License

MIT © Jamal Dawson
