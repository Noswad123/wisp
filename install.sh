#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
bin_dir="${WISP_BIN_DIR:-$HOME/.local/bin}"
zsh_completion_dir="${WISP_ZSH_COMPLETION_DIR:-$HOME/.local/share/zsh/site-functions}"
bash_completion_dir="${WISP_BASH_COMPLETION_DIR:-$HOME/.local/share/bash-completion/completions}"
install_completions="${WISP_INSTALL_COMPLETIONS:-1}"

mkdir -p "$bin_dir"
install -m 0755 "$repo_root/bin/wisp" "$bin_dir/wisp"
printf 'installed wisp -> %s/wisp\n' "$bin_dir"

if [[ "$install_completions" != "0" ]]; then
  mkdir -p "$zsh_completion_dir" "$bash_completion_dir"
  install -m 0644 "$repo_root/completions/zsh/_wisp" "$zsh_completion_dir/_wisp"
  install -m 0644 "$repo_root/completions/bash/wisp" "$bash_completion_dir/wisp"
  printf 'installed zsh completion -> %s/_wisp\n' "$zsh_completion_dir"
  printf 'installed bash completion -> %s/wisp\n' "$bash_completion_dir"
fi
