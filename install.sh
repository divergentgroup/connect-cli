#!/bin/sh
# Installs the latest connect release. Usage: curl --proto '=https' --proto-redir '=https' -fsSL https://connect.ms/install.sh | sh
# Set CONNECT_VERSION (e.g. 1.2.3) to install that release instead of the latest.
set -eu
REPO="divergentgroup/connect-cli"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in darwin|linux) ;; *) echo "unsupported OS: $os; download from https://github.com/$REPO/releases" >&2; exit 1 ;; esac

if [ -n "${CONNECT_VERSION:-}" ]; then tag="v${CONNECT_VERSION#v}"; else tag=$(curl --proto '=https' --proto-redir '=https' -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1); fi
[ -n "$tag" ] || { echo "could not determine the latest release" >&2; exit 1; }
version=${tag#v}
archive="connect_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
curl --proto '=https' --proto-redir '=https' -fsSL -o "$tmp/$archive" "$base/$archive"
curl --proto '=https' --proto-redir '=https' -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
expected=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp/$archive" | cut -d' ' -f1); else actual=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1); fi
[ "$expected" = "$actual" ] || { echo "checksum mismatch for $archive" >&2; exit 1; }
tar -xzf "$tmp/$archive" -C "$tmp" connect

if [ -w /usr/local/bin ]; then dest=/usr/local/bin; else dest="$HOME/.local/bin"; mkdir -p "$dest"; fi
install -m 0755 "$tmp/connect" "$dest/connect"
echo "Installed connect $version to $dest/connect"
case ":$PATH:" in *":$dest:"*) ;; *) echo "Add $dest to your PATH." ;; esac
