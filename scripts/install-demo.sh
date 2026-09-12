#!/bin/sh
# install-demo.sh — one-line installer for the `doppler-agent` preview build (the CLI
# fork that bundles the agent-proxy), served from GCS. Deliberately minimal compared to
# the production scripts/install.sh: no package managers, no GPG — just fetch the archive
# for this OS/arch, verify its sha256, and drop `doppler-agent` on PATH.
#
#   curl -fsSL https://storage.googleapis.com/doppler-proxy-alpha/install.sh | sh
#
# It installs `doppler-agent` (never `doppler`), so it can't collide with a production
# Doppler CLI. Its CLI config lives in ~/.doppler-agent; the proxy keeps its own data
# (CA, agent env, logs) in ~/.config/agent-proxy, or ~/Library/Application Support/
# agent-proxy on macOS.
set -eu

BUCKET="${DOPPLER_AGENT_BUCKET:-doppler-proxy-alpha}"
# Base URL the artifacts are served from. Override to point at a mirror, a signed-URL
# host, or a local server for testing; defaults to the public GCS bucket.
BASE="${DOPPLER_AGENT_BASE_URL:-https://storage.googleapis.com/${BUCKET}/doppler-agent}"
INSTALL_DIR="${DOPPLER_AGENT_INSTALL_DIR:-/usr/local/bin}"

log() { printf '%s\n' "$*" >&2; }
fail() { log "ERROR: $*"; exit 1; }

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

# Enforce TLS for real (https) downloads. The non-https branch is a TESTING HOOK ONLY
# (a local server or CI): it drops TLS, and because the checksum is fetched from the same
# base it can't detect a hostile mirror — never point DOPPLER_AGENT_BASE_URL at an
# untrusted non-https host.
case "$BASE" in
  https://*) DL="curl -fsSL --proto =https --tlsv1.2" ;;
  *)         DL="curl -fsSL" ;; # testing only — see the note above
esac

# --- OS ---
case "$(uname -s)" in
  Darwin) os="macOS" ;;                 # matches the goreleaser archive name for darwin
  Linux)  os="linux" ;;
  *) fail "unsupported OS '$(uname -s)' (this build ships macOS and Linux only)" ;;
esac

# --- arch ---
case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported architecture '$(uname -m)' (this build ships amd64 and arm64 only)" ;;
esac

# --- version: an explicit override, else the `latest` marker the release workflow writes ---
version="${DOPPLER_AGENT_VERSION:-}"
[ -n "$version" ] || version="$($DL "${BASE}/latest")" || fail "could not read the latest version from ${BASE}/latest"
version="${version#v}" # goreleaser paths/names use the version without a leading 'v'

archive="doppler-agent_${version}_${os}_${arch}.tar.gz"
url="${BASE}/${version}/${archive}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "Downloading ${archive} …"
$DL "$url" -o "${tmp}/${archive}" || fail "download failed: $url"

# --- verify checksum (fail closed: no verifiable checksum means no install) ---
$DL "${BASE}/${version}/checksums.txt" -o "${tmp}/checksums.txt" || fail "could not download checksums.txt to verify ${archive}"
want="$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
[ -n "$want" ] || fail "no checksum listed for ${archive} in checksums.txt"
got="$( (command -v sha256sum >/dev/null 2>&1 && sha256sum "${tmp}/${archive}" || shasum -a 256 "${tmp}/${archive}") | awk '{print $1}')"
[ "$want" = "$got" ] || fail "checksum mismatch for ${archive} (want ${want}, got ${got})"
log "Checksum verified."

tar -xzf "${tmp}/${archive}" -C "$tmp" doppler-agent || fail "could not extract doppler-agent from the archive"

# --- install, falling back to a user-writable dir if the default needs root ---
if [ ! -w "$INSTALL_DIR" ] && [ "$(id -u)" -ne 0 ]; then
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
  log "No write access to /usr/local/bin; installing to ${INSTALL_DIR} (make sure it's on your PATH)."
fi
install -m 0755 "${tmp}/doppler-agent" "${INSTALL_DIR}/doppler-agent" || fail "could not install to ${INSTALL_DIR}"

log "Installed doppler-agent ${version} to ${INSTALL_DIR}/doppler-agent"
log "Run: doppler-agent proxy start"
