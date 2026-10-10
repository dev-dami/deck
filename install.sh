#!/bin/sh
# Installs deck on macOS or Linux from the latest GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/dev-dami/deck/main/install.sh | sh
#
# Settings (environment):
#   DECK_VERSION      a release tag to install, e.g. v0.7.0 (default: the latest)
#   DECK_INSTALL_DIR  where the deck binary goes (default: ~/.local/bin)
#   DECK_REPO         the GitHub repository to install from (default: dev-dami/deck)
set -eu

REPO="${DECK_REPO:-dev-dami/deck}"
DIR="${DECK_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'deck install: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "needs $1"; }
need curl
need tar
need uname

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported system $(uname -s); on Windows use install.ps1" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported processor $(uname -m)" ;;
esac

tag="${DECK_VERSION:-}"
if [ -z "$tag" ]; then
  # The latest release page redirects to its tag.
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')
  case "$tag" in
    v*) ;;
    *) fail "could not find the latest release of $REPO" ;;
  esac
fi
version="${tag#v}"

archive="deck_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading deck $tag for $os/$arch"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "could not download $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download the checksums"

want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || fail "$archive is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
else
  fail "needs sha256sum or shasum to check the download"
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/deck" ] || fail "the archive has no deck binary"

mkdir -p "$DIR"
mv "$tmp/deck" "$DIR/deck"
chmod +x "$DIR/deck"

say "Installed deck $tag to $DIR/deck"
case ":$PATH:" in
  *":$DIR:"*) ;;
  *)
    say ""
    say "$DIR is not on your PATH. Add this line to your shell profile (~/.bashrc, ~/.zshrc):"
    say "  export PATH=\"$DIR:\$PATH\""
    ;;
esac
say ""
say "Deck drives the agent CLIs you have installed and signed in to: claude, codex, opencode."
say "Run 'deck doctor' to see which ones it found, then 'deck' in a project folder."
