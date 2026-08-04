#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <goos> <goarch> <output-dir>" >&2
  exit 1
fi

GOOS_TARGET="$1"
GOARCH_TARGET="$2"
OUTPUT_DIR="$3"
CODEX_CLI_DOWNLOAD_BASE_URL="${CODEX_CLI_DOWNLOAD_BASE_URL:-https://csgclaw.opencsg.com/codex-cli/latest}"

resolve_platform() {
  case "$1/$2" in
    linux/amd64) printf '%s\n' 'linux amd64 codex-x86_64-unknown-linux-musl' ;;
    linux/arm64) printf '%s\n' 'linux arm64 codex-aarch64-unknown-linux-musl' ;;
    darwin/amd64) printf '%s\n' 'macos x64 codex-x86_64-apple-darwin' ;;
    darwin/arm64) printf '%s\n' 'macos arm64 codex-aarch64-apple-darwin' ;;
    windows/amd64) printf '%s\n' 'windows amd64 codex.exe' ;;
    windows/arm64) printf '%s\n' 'windows arm64 codex.exe' ;;
    *)
      echo "unsupported bundled Codex CLI target: $1/$2" >&2
      exit 1
      ;;
  esac
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

read -r download_os download_arch archive_binary <<<"$(resolve_platform "$GOOS_TARGET" "$GOARCH_TARGET")"
download_url="${CODEX_CLI_DOWNLOAD_BASE_URL%/}/${download_os}/${download_arch}?package=codex-cli"

need_cmd curl
need_cmd mktemp
if [ "$GOOS_TARGET" != "windows" ]; then
  need_cmd tar
  need_cmd install
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

mkdir -p "$OUTPUT_DIR"
echo "fetching bundled Codex CLI ${download_url}"

if [ "$GOOS_TARGET" = "windows" ]; then
  curl -fsSL "$download_url" -o "$OUTPUT_DIR/codex.exe"
  test -s "$OUTPUT_DIR/codex.exe" || {
    echo "downloaded Codex executable is empty" >&2
    exit 1
  }
  exit 0
fi

archive_path="$tmpdir/codex.tar.gz"
extract_dir="$tmpdir/extract"
mkdir -p "$extract_dir"
curl -fsSL "$download_url" -o "$archive_path"
tar -xzf "$archive_path" -C "$extract_dir"

binary_path="$extract_dir/$archive_binary"
if [ ! -f "$binary_path" ]; then
  echo "Codex archive did not contain expected binary: $archive_binary" >&2
  exit 1
fi
install -m 0755 "$binary_path" "$OUTPUT_DIR/codex"
