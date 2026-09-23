#!/bin/sh
set -eu

REPO=${KTOP_REPO:-miraxmetov/ktop}
VERSION=${KTOP_VERSION:-latest}
BASE_URL=${KTOP_BASE_URL:-}
PREFIX=${PREFIX:-$HOME/.local}
BIN=$PREFIX/bin

XDG_DATA=${XDG_DATA_HOME:-$HOME/.local/share}
XDG_CONF=${XDG_CONFIG_HOME:-$HOME/.config}
FISH_COMP=$XDG_CONF/fish/completions/ktop.fish
BASH_COMP=$XDG_DATA/bash-completion/completions/ktop
ZSH_COMP=$XDG_DATA/zsh/site-functions/_ktop

say() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die() { warn "install: $*"; exit 1; }

usage() {
    say "Usage: install.sh [--uninstall] [--from-source]"
    say ""
    say "  KTOP_REPO=<owner/repo>   GitHub repository (default: $REPO)"
    say "  KTOP_VERSION=<tag>       release to install (default: latest)"
    say "  KTOP_BASE_URL=<url>      download assets from a mirror instead of GitHub"
    say "  PREFIX=<dir>             install prefix (default: \$HOME/.local)"
}

uninstall() {
    for path in "$BIN/ktop" "$FISH_COMP" "$BASH_COMP" "$ZSH_COMP"; do
        if [ -e "$path" ]; then
            rm -f "$path"
            say "removed $path"
        fi
    done
    exit 0
}

FROM_SOURCE=no
case "${1:-}" in
    -u|--uninstall) uninstall ;;
    -h|--help) usage; exit 0 ;;
    -s|--from-source) FROM_SOURCE=yes ;;
    "") ;;
    *) warn "install: unknown option $1"; usage; exit 2 ;;
esac

detect_platform() {
    _os=$(uname -s | tr '[:upper:]' '[:lower:]')
    _arch=$(uname -m)
    case "$_arch" in
        x86_64|amd64) _arch=amd64 ;;
        aarch64|arm64) _arch=arm64 ;;
        *) warn "no prebuilt binary for architecture $_arch"; return 1 ;;
    esac
    case "$_os" in
        darwin|linux) ;;
        *) warn "no prebuilt binary for system $_os"; return 1 ;;
    esac
    TARGET="${_os}_${_arch}"
}

fetch() {
    _url=$1
    _out=$2
    if command -v curl >/dev/null 2>&1; then
        curl -fsL "$_url" -o "$_out"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$_url" -O "$_out"
    else
        die "neither curl nor wget found"
    fi
}

checksum() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    fi
}

install_file() {
    _from=$1
    _to=$2
    _mode=$3
    mkdir -p "$(dirname "$_to")"
    cp "$_from" "$_to"
    chmod "$_mode" "$_to"
    say "installed $_to"
}

install_completions() {
    _comp=$1
    [ -d "$_comp" ] || return 0
    if [ -f "$_comp/ktop.fish" ] && { command -v fish >/dev/null 2>&1 || [ -d "$XDG_CONF/fish" ]; }; then
        install_file "$_comp/ktop.fish" "$FISH_COMP" 644
    fi
    if [ -f "$_comp/ktop.bash" ] && command -v bash >/dev/null 2>&1; then
        install_file "$_comp/ktop.bash" "$BASH_COMP" 644
    fi
    if [ -f "$_comp/_ktop" ] && command -v zsh >/dev/null 2>&1; then
        install_file "$_comp/_ktop" "$ZSH_COMP" 644
    fi
}

build_from_source() {
    srcdir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
    [ -f "$srcdir/go.mod" ] || die "no Go sources next to install.sh, clone the repository first"
    command -v go >/dev/null 2>&1 || die "go toolchain not found, install Go or use a release build"
    say "building ktop from source"
    ( cd "$srcdir" && CGO_ENABLED=0 go build -ldflags "-s -w" -o "$TMP/ktop" ./cmd/ktop )
    install_file "$TMP/ktop" "$BIN/ktop" 755
    install_completions "$srcdir/completions"
}

install_release() {
    detect_platform || return 1
    asset="ktop_${TARGET}.tar.gz"
    if [ -n "$BASE_URL" ]; then
        release_url=$BASE_URL
        release_name=$BASE_URL
    else
        release_url="https://github.com/$REPO/releases/download/$VERSION"
        if [ "$VERSION" = latest ]; then
            release_url="https://github.com/$REPO/releases/latest/download"
        fi
        release_name="$REPO ($VERSION)"
    fi

    say "downloading $asset from $release_name"
    if ! fetch "$release_url/$asset" "$TMP/$asset"; then
        return 1
    fi

    if fetch "$release_url/checksums.txt" "$TMP/checksums.txt" 2>/dev/null; then
        want=$(grep " $asset\$" "$TMP/checksums.txt" | awk '{print $1}' | head -1)
        got=$(checksum "$TMP/$asset")
        if [ -z "$got" ]; then
            warn "note: no sha256 tool found, skipping checksum verification"
        elif [ -z "$want" ]; then
            warn "note: $asset missing from checksums.txt, skipping verification"
        elif [ "$want" != "$got" ]; then
            die "checksum mismatch for $asset"
        else
            say "checksum ok"
        fi
    else
        warn "note: checksums.txt unavailable, skipping verification"
    fi

    mkdir -p "$TMP/unpack"
    tar -xzf "$TMP/$asset" -C "$TMP/unpack"
    [ -f "$TMP/unpack/ktop" ] || die "archive does not contain a ktop binary"

    install_file "$TMP/unpack/ktop" "$BIN/ktop" 755
    install_completions "$TMP/unpack/completions"
}

TMP=$(mktemp -d "${TMPDIR:-/tmp}/ktop.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM

mkdir -p "$BIN" || die "cannot create $BIN"
[ -w "$BIN" ] || die "$BIN is not writable, set PREFIX=<dir> or rerun with sudo"

if [ "$FROM_SOURCE" = yes ]; then
    build_from_source
elif ! install_release; then
    warn "release download failed, falling back to a source build"
    build_from_source
fi

if [ -f "$ZSH_COMP" ]; then
    case "${FPATH:-}" in
        *"$(dirname "$ZSH_COMP")"*) ;;
        *)
            say ""
            say "For zsh completions add to ~/.zshrc:"
            say "  fpath=($(dirname "$ZSH_COMP") \$fpath)"
            say "  autoload -Uz compinit && compinit"
            ;;
    esac
fi

case ":$PATH:" in
    *":$BIN:"*) ;;
    *)
        say ""
        say "$BIN is not in PATH. Add it:"
        case "$(basename "${SHELL:-sh}")" in
            fish) say "  fish_add_path $BIN" ;;
            csh|tcsh) say "  set path = ($BIN \$path)" ;;
            *) say "  export PATH=\"$BIN:\$PATH\"" ;;
        esac
        ;;
esac

say ""
say "ktop installed. Run: ktop <namespace>"
