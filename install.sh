#!/bin/sh
# Installs deck on macOS or Linux from the latest GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.sh | sh
#
# Settings (environment):
#   DECK_VERSION      a release tag to install, e.g. v0.7.0 (default: the latest)
#   DECK_INSTALL_DIR  where the deck binary goes (default: ~/.local/bin)
#   DECK_REPO         the GitHub repository to install from (default: dev-dami/deck-releases)
set -eu

REPO="${DECK_REPO:-dev-dami/deck-releases}"
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

# A private repository answers plain downloads with 404; the GitHub CLI, when
# it is installed and signed in, still can.
have_gh() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

tag="${DECK_VERSION:-}"
if [ -z "$tag" ]; then
  # The latest release page redirects to its tag.
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null | sed 's#.*/tag/##') || tag=""
  case "$tag" in
    v*) ;;
    *)
      if have_gh; then
        tag=$(gh release view --repo "$REPO" --json tagName -q .tagName 2>/dev/null) || tag=""
      fi
      case "$tag" in
        v*) ;;
        *) fail "could not find the latest release of $REPO (if it is private, install gh and run 'gh auth login')" ;;
      esac
      ;;
  esac
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
version="${tag#v}"

# Archive names carry build metadata with a "-" in place of "+".
archive="deck_$(printf '%s' "$version" | tr '+' '-')_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$(printf '%s' "$tag" | sed 's/+/%2B/g')"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading deck $tag for $os/$arch"
if ! curl -fsSL -o "$tmp/$archive" "$base/$archive" 2>/dev/null ||
  ! curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" 2>/dev/null; then
  have_gh || fail "could not download $base/$archive (if the repository is private, install gh and run 'gh auth login')"
  rm -f "$tmp/$archive" "$tmp/checksums.txt"
  gh release download "$tag" --repo "$REPO" --pattern "$archive" --pattern checksums.txt --dir "$tmp" ||
    fail "could not download $archive from $REPO $tag"
fi

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
say "Deck drives the agent CLIs you have installed and signed in to: claude, codex, opencode, agy."
say "Run 'deck doctor' to see which ones it found, then 'deck' in a project folder."
