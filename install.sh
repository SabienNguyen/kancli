#!/bin/sh
# Install kancli on Linux, macOS or FreeBSD.
#
#   curl -fsSL https://raw.githubusercontent.com/SabienNguyen/kancli/main/install.sh | sh
#
# Downloads the latest release binary for your platform into ~/.local/bin.
# If there is no release for your platform (or no release yet), it falls
# back to `go install`, which needs Go 1.25 or newer. Set KANCLI_BINDIR to
# install somewhere else, or KANCLI_VERSION to pin a release tag.
set -eu

REPO="SabienNguyen/kancli"
BINDIR="${KANCLI_BINDIR:-$HOME/.local/bin}"
VERSION="${KANCLI_VERSION:-}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin|freebsd) ;;
  *) echo "unsupported OS: $os (use install.ps1 on Windows)" >&2; exit 1 ;;
esac

mkdir -p "$BINDIR"

fetch() { # url -> stdout
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then wget -qO- "$1"
  else return 1; fi
}

go_install() {
  if ! command -v go >/dev/null 2>&1; then
    echo "no release binary available and Go is not installed." >&2
    echo "install Go from https://go.dev/dl/ and rerun, or clone the repo and run 'make install'." >&2
    exit 1
  fi
  echo "building with go install ..."
  GOBIN="$BINDIR" go install "github.com/$REPO/cmd/kancli@${VERSION:-latest}"
}

if [ -z "$VERSION" ]; then
  VERSION=$(fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1 || true)
fi

if [ -n "$VERSION" ]; then
  file="kancli_${VERSION#v}_${os}_${arch}.tar.gz"
  url="https://github.com/$REPO/releases/download/$VERSION/$file"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  if fetch "$url" > "$tmp/$file" 2>/dev/null && tar -xzf "$tmp/$file" -C "$tmp" kancli 2>/dev/null; then
    install -m 0755 "$tmp/kancli" "$BINDIR/kancli"
    echo "installed kancli $VERSION to $BINDIR/kancli"
  else
    echo "no release asset for $os/$arch at $VERSION; falling back to go install" >&2
    go_install
  fi
else
  echo "no release found; falling back to go install" >&2
  go_install
fi

case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *) echo "add $BINDIR to your PATH, for example:"; echo "  export PATH=\"$BINDIR:\$PATH\"" ;;
esac
echo "run: kancli -demo"
