#!/usr/bin/env bash
# pcke installer — downloads the latest (or specified) release binary.
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/jenaiz/pcke/main/install.sh | sh
#   VERSION=v1.2.1 curl -sSfL .../install.sh | sh
#   INSTALL_DIR=/usr/local/bin curl -sSfL .../install.sh | sh
#
# Environment variables:
#   VERSION      — release tag to install (default: latest)
#   INSTALL_DIR  — destination directory (default: $HOME/.local/bin)

set -euo pipefail

REPO="jenaiz/pcke"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# --- helpers ----------------------------------------------------------------

log() { printf '[pcke-install] %s\n' "$*"; }
err() { printf '[pcke-install] ERROR: %s\n' "$*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

# --- detect platform --------------------------------------------------------

detect_os() {
  case "$(uname -s)" in
    Darwin)  echo "darwin" ;;
    Linux)   echo "linux" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *)       err "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    aarch64|arm64)   echo "arm64" ;;
    *)               err "unsupported architecture: $(uname -m)" ;;
  esac
}

# --- resolve version --------------------------------------------------------

resolve_version() {
  if [ -n "${VERSION:-}" ]; then
    echo "$VERSION"
    return
  fi
  need_cmd curl
  local latest
  latest=$(curl -sSfL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  [ -n "$latest" ] || err "could not determine latest release"
  echo "$latest"
}

# --- main -------------------------------------------------------------------

main() {
  need_cmd curl
  need_cmd sha256sum || need_cmd shasum
  need_cmd tar

  local os arch version
  os="$(detect_os)"
  arch="$(detect_arch)"
  version="$(resolve_version)"

  log "installing pcke ${version} (${os}/${arch})"

  local name="pcke_${version#v}_${os}_${arch}"
  local ext="tar.gz"
  if [ "$os" = "windows" ]; then
    ext="zip"
  fi
  local archive="${name}.${ext}"
  local base_url="https://github.com/${REPO}/releases/download/${version}"

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  log "downloading ${archive}..."
  curl -sSfL -o "${tmpdir}/${archive}" "${base_url}/${archive}"

  log "downloading checksums.txt..."
  curl -sSfL -o "${tmpdir}/checksums.txt" "${base_url}/checksums.txt"

  log "verifying checksum..."
  local expected actual
  expected=$(grep "${archive}" "${tmpdir}/checksums.txt" | awk '{print $1}')
  [ -n "$expected" ] || err "checksum not found for ${archive}"

  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmpdir}/${archive}" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "${tmpdir}/${archive}" | awk '{print $1}')
  fi

  if [ "$expected" != "$actual" ]; then
    err "checksum mismatch: expected ${expected}, got ${actual}"
  fi
  log "checksum OK"

  log "extracting..."
  if [ "$ext" = "zip" ]; then
    need_cmd unzip
    unzip -q "${tmpdir}/${archive}" -d "${tmpdir}/out"
  else
    mkdir -p "${tmpdir}/out"
    tar -xzf "${tmpdir}/${archive}" -C "${tmpdir}/out"
  fi

  mkdir -p "$INSTALL_DIR"
  install -m 755 "${tmpdir}/out/pcke" "${INSTALL_DIR}/pcke"

  log "installed: ${INSTALL_DIR}/pcke"
  "${INSTALL_DIR}/pcke" --version

  # PATH hint
  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
      log ""
      log "Add ${INSTALL_DIR} to your PATH:"
      log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      ;;
  esac
}

main "$@"
