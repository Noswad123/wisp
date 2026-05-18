#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
bin_dir="${WISP_BIN_DIR:-$HOME/.local/bin}"

mkdir -p "$bin_dir"
install -m 0755 "$repo_root/bin/wisp" "$bin_dir/wisp"
printf 'installed wisp -> %s/wisp\n' "$bin_dir"
