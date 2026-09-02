#!/bin/sh
# Build kancli from this checkout and put it on your PATH.
#
#   git clone https://github.com/SabienNguyen/kancli.git
#   cd kancli
#   ./install.sh
#
# Installs to ~/.local/bin (override with KANCLI_BINDIR). Pass --path to
# also append that directory to your shell's startup file when it is not
# on your PATH yet. Needs Go; if your Go is older than go.mod asks for, Go
# fetches the right toolchain itself.
set -eu
cd "$(dirname "$0")"

BINDIR="${KANCLI_BINDIR:-$HOME/.local/bin}"
ADD_PATH=no
for arg in "$@"; do
  case "$arg" in
    --path) ADD_PATH=yes ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

if ! command -v go >/dev/null 2>&1; then
  echo "Go is not installed. Install it and rerun:" >&2
  case "$(uname -s)" in
    Darwin) echo "  brew install go        (or https://go.dev/dl/)" >&2 ;;
    Linux)  echo "  https://go.dev/dl/     (distro packages are often too old)" >&2 ;;
    *)      echo "  https://go.dev/dl/" >&2 ;;
  esac
  exit 1
fi

version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
mkdir -p "$BINDIR"
echo "building kancli $version ..."
go build -ldflags "-s -w -X main.version=$version" -o "$BINDIR/kancli" ./cmd/kancli
echo "installed $BINDIR/kancli"

case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *)
    line="export PATH=\"$BINDIR:\$PATH\""
    rc=""
    case "${SHELL:-}" in
      */zsh)  rc="$HOME/.zshrc" ;;
      */bash) rc="$HOME/.bashrc" ;;
      */fish) rc="$HOME/.config/fish/config.fish"; line="fish_add_path $BINDIR" ;;
    esac
    if [ "$ADD_PATH" = yes ] && [ -n "$rc" ]; then
      printf '\n# kancli\n%s\n' "$line" >> "$rc"
      echo "added $BINDIR to PATH in $rc (open a new terminal)"
    else
      echo "$BINDIR is not on your PATH. Add it with:"
      echo "  $line"
      [ -n "$rc" ] && echo "or rerun with ./install.sh --path to append that to $rc"
    fi
    ;;
esac
echo "try it: kancli -demo"
