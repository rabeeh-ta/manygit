#!/usr/bin/env bash
# manygit installer. Usage:
#   curl -fsSL https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.sh | bash
set -euo pipefail

repo="rabeeh-ta/manygit"
bin="manygit"
install_dir="${MANYGIT_INSTALL_DIR:-$HOME/.local/bin}"

die() { printf 'error: %s\n' "$1" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar  >/dev/null 2>&1 || die "tar is required"

os=$(uname -s)
case "$os" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS: $os (manygit supports Linux and macOS)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" \
      | grep '"tag_name"' | head -1 | cut -d'"' -f4)
[ -n "$tag" ] || die "no published release found for $repo yet"

asset="${bin}_${os}_${arch}.tar.gz"
url="https://github.com/$repo/releases/download/$tag/$asset"

printf 'Installing %s %s (%s/%s)...\n' "$bin" "$tag" "$os" "$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/$asset" || die "download failed: $url"
tar -xzf "$tmp/$asset" -C "$tmp"   || die "could not extract $asset"
[ -f "$tmp/$bin" ] || die "archive did not contain $bin"

mkdir -p "$install_dir"
mv "$tmp/$bin" "$install_dir/$bin"
chmod +x "$install_dir/$bin"
printf 'Installed to %s\n' "$install_dir/$bin"

# Put install_dir on PATH if it isn't already.
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    case "${SHELL:-}" in
      */zsh) rc="$HOME/.zshrc" ;;
      *)     rc="$HOME/.bashrc" ;;
    esac
    printf '\n# manygit\nexport PATH="%s:$PATH"\n' "$install_dir" >> "$rc"
    printf 'Added %s to your PATH in %s. Restart your shell (or open a new tab) to pick it up.\n' "$install_dir" "$rc"
    ;;
esac

printf 'Done. Run: %s\n' "$bin"
