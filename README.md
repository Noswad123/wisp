# wisp

`wisp` opens a command in a floating kitty terminal window.

It is part of the Jamal Arcana ecosystem, but it is intentionally generic: `wisp` floats whatever command it receives and does not know about any specific companion tool.

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
wisp nvim README.md
wisp yazi ~/Downloads
wisp waystone nvim
```

## Environment

| Variable | Purpose |
| --- | --- |
| `WISP_TITLE` | Override floating window title |
| `WISP_DIR` | Override working directory |
| `WISP_PATH` | Path used to infer title and working directory |

## Dependencies

- macOS `open`
- kitty
- optional: Aerospace, for post-open floating layout adjustment

## License

MIT © Jamal Dawson
