#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
bin_dir="${WISP_BIN_DIR:-$HOME/.local/bin}"
zsh_completion_dir="${WISP_ZSH_COMPLETION_DIR:-$HOME/.local/share/zsh/site-functions}"
bash_completion_dir="${WISP_BASH_COMPLETION_DIR:-$HOME/.local/share/bash-completion/completions}"
install_completions="${WISP_INSTALL_COMPLETIONS:-1}"

mkdir -p "$bin_dir"
tmp_bin="$(mktemp "${TMPDIR:-/tmp}/wisp.XXXXXX")"
trap 'rm -f "$tmp_bin"' EXIT
(cd "$repo_root" && go build -o "$tmp_bin" ./cmd/wisp)
install -m 0755 "$tmp_bin" "$bin_dir/wisp"
ln -sf "wisp" "$bin_dir/wispd"
printf 'installed wisp -> %s/wisp\n' "$bin_dir"
printf 'installed wispd -> %s/wispd\n' "$bin_dir"

if [[ "$(uname -s)" == "Darwin" ]] && command -v swiftc >/dev/null 2>&1; then
  tmp_hint="$(mktemp "${TMPDIR:-/tmp}/wisp-hint-macos.XXXXXX")"
  trap 'rm -f "$tmp_bin" "$tmp_hint"' EXIT
  (cd "$repo_root" && swiftc -O -o "$tmp_hint" ./cmd/wisp-hint-macos/main.swift)
  install -m 0755 "$tmp_hint" "$bin_dir/wisp-hint-macos"
  printf 'installed wisp-hint-macos -> %s/wisp-hint-macos\n' "$bin_dir"
fi

if [[ "$install_completions" != "0" ]]; then
  mkdir -p "$zsh_completion_dir" "$bash_completion_dir"
  install -m 0644 "$repo_root/completions/zsh/_wisp" "$zsh_completion_dir/_wisp"
  install -m 0644 "$repo_root/completions/bash/wisp" "$bash_completion_dir/wisp"
  printf 'installed zsh completion -> %s/_wisp\n' "$zsh_completion_dir"
  printf 'installed bash completion -> %s/wisp\n' "$bash_completion_dir"
fi
