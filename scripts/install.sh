#!/bin/sh
# Installs the latest booksy release into ~/.local/bin (or $BOOKSY_INSTALL_DIR).
#
#   curl -fsSL https://booksy.gimeno.dev/install.sh | sh
#
# BOOKSY_VERSION=v0.2.0 pins a version. With a logged-in gh CLI the script
# downloads through it instead of curl, which also works for a private fork.
set -eu
repo="GodGMN/booksy-cli"
dir="${BOOKSY_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux|darwin) ;; *) echo "booksy: unsupported OS $os; on Windows download the zip from the releases page" >&2; exit 1 ;; esac
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "booksy: unsupported CPU $arch" >&2; exit 1 ;; esac

use_gh=false
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then use_gh=true; fi

tag="${BOOKSY_VERSION:-}"
if [ -z "$tag" ]; then
  if $use_gh; then
    tag=$(gh release view --repo "$repo" --json tagName -q .tagName 2>/dev/null || true)
  else
    tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' || true)
  fi
fi
[ -n "$tag" ] || { echo "booksy: no release found (if the repository is private, install gh and run gh auth login)" >&2; exit 1; }
version=${tag#v}
asset="booksy_${version}_${os}_${arch}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if $use_gh; then
  gh release download "$tag" --repo "$repo" --dir "$tmp" --pattern "$asset" --pattern checksums.txt
else
  curl -fsSL "https://github.com/$repo/releases/download/$tag/$asset" -o "$tmp/$asset"
  curl -fsSL "https://github.com/$repo/releases/download/$tag/checksums.txt" -o "$tmp/checksums.txt"
fi

# macOS ships shasum, most Linux distributions ship sha256sum.
if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi
(cd "$tmp" && grep " $asset\$" checksums.txt | $sum -c - >/dev/null) || { echo "booksy: checksum mismatch for $asset" >&2; exit 1; }

tar -xzf "$tmp/$asset" -C "$tmp" booksy
mkdir -p "$dir"
install -m 0755 "$tmp/booksy" "$dir/booksy"
echo "installed booksy $version to $dir/booksy"
case ":$PATH:" in *":$dir:"*) ;; *) echo "add $dir to your PATH, e.g.: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" ;; esac
