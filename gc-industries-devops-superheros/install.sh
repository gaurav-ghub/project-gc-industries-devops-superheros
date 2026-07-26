#!/usr/bin/env bash
#
# Endurance CLI installer.
#
#   curl -fsSL https://raw.githubusercontent.com/gc-ghub/project-gc-industries-devops-superheros/main/gc-industries-devops-superheros/install.sh | bash
#
# It downloads one file — the `endurance` binary for this machine — checks it
# against the release's published sha256, puts it on PATH, and runs it to prove
# it works. It installs nothing else: Docker, kind, kubectl, helm and istioctl
# are the platform's prerequisites, and `endurance doctor` already reports which
# of them are missing, in the tool's own voice and with the reason each is
# needed. An installer that also installed those would be a second, worse
# doctor.
#
# It does not use sudo, does not touch a cluster, and does not write anything
# outside the directory it installs into.
#
# ---------------------------------------------------------------------------
# On the look of this file
#
# This is the one script in the project that runs *before* the Go binary exists,
# so it has no `internal/render` to reuse — and it does not grow a copy of one.
# It prints plain facts, one per line, with `error:` and `warning:` on stderr:
# exactly the contract platform/lib/logger.sh has followed since Phase 8. There
# are three visual systems in this project (the Go renderer, plain bash, and
# GitHub's own output) and there is not going to be a fourth.
# ---------------------------------------------------------------------------
#
# Environment:
#   ENDURANCE_VERSION      release tag to install (default: the latest release)
#   ENDURANCE_INSTALL_DIR  where to put the binary (default: chosen from PATH)
#   ENDURANCE_BASE_URL     override the download location (used by the tests)
#   ENDURANCE_LIB          set to 1 to source this file without installing
#                          anything, so its functions can be unit-tested

set -euo pipefail

REPO="gc-ghub/project-gc-industries-devops-superheros"
RELEASES_URL="https://github.com/${REPO}/releases"
CHECKSUMS="checksums.txt"
BINARY="endurance"

# ---------------------------------------------------------------------------
# Output. Facts on stdout, problems on stderr.
# ---------------------------------------------------------------------------

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
  printf 'error: %s\n' "$1" >&2
  shift
  for line in "$@"; do printf '  %s\n' "$line" >&2; done
  exit 1
}

# ---------------------------------------------------------------------------
# Which machine is this
# ---------------------------------------------------------------------------

# detect_os maps `uname -s` onto the GOOS the release was built for.
#
# git-bash reports MINGW64_NT-10.0-26200, MSYS reports MSYS_NT-…, and Cygwin
# reports CYGWIN_NT-… . All three run Windows binaries, which is why the
# windows asset carries a .exe suffix and the others do not.
detect_os() {
  case "$(uname -s)" in
    Linux*) echo linux ;;
    Darwin*) echo darwin ;;
    MINGW* | MSYS* | CYGWIN*) echo windows ;;
    *) echo "" ;;
  esac
}

# detect_arch maps `uname -m` onto the GOARCH the release was built for.
detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) echo "" ;;
  esac
}

# asset_name is the published file name for an os/arch pair, or nothing when the
# release does not build one. These names are pinned by
# cli/internal/installer/installer.go and asserted against this function by
# TestTheScriptAndTheGoTableNameTheSameAssets.
asset_name() {
  case "$1/$2" in
    windows/amd64) echo endurance_windows_amd64.exe ;;
    darwin/arm64) echo endurance_darwin_arm64 ;;
    darwin/amd64) echo endurance_darwin_amd64 ;;
    linux/amd64) echo endurance_linux_amd64 ;;
    linux/arm64) echo endurance_linux_arm64 ;;
    *) echo "" ;;
  esac
}

# installed_name is what the binary is called once it is on PATH. Windows will
# not execute a file without the extension.
installed_name() {
  if [ "$1" = windows ]; then echo "${BINARY}.exe"; else echo "$BINARY"; fi
}

# base_url is where the assets are fetched from. An empty ENDURANCE_VERSION
# means the latest release, which GitHub serves from a fixed path by redirect —
# no API call, no token, no rate limit.
base_url() {
  if [ -n "${ENDURANCE_BASE_URL:-}" ]; then
    echo "${ENDURANCE_BASE_URL%/}"
  elif [ -z "${1:-}" ]; then
    echo "${RELEASES_URL}/latest/download"
  else
    echo "${RELEASES_URL}/download/$1"
  fi
}

# ---------------------------------------------------------------------------
# Downloading, and checking what came back
# ---------------------------------------------------------------------------

fetch() {
  # -f so a 404 is a failure rather than a downloaded error page, and -L
  # because /releases/latest/download is a redirect.
  curl -fsSL --retry 2 --retry-delay 1 -o "$2" "$1"
}

# sha256_of prints the hex digest of a file using whichever of the three usual
# tools this machine has.
#
# It is deliberately possible for this to find nothing, and the caller treats
# that as fatal rather than as permission to skip the check. An installer that
# silently stops verifying when the tool is missing verifies nothing — the
# machines without sha256sum are exactly the ones nobody tested on.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    echo ""
  fi
}

# expected_sha reads one asset's digest out of a sha256sum-format manifest.
expected_sha() {
  awk -v want="$2" '$2 == want || $2 == "*" want { print $1; exit }' "$1"
}

# ---------------------------------------------------------------------------
# Where it goes
# ---------------------------------------------------------------------------

on_path() {
  case ":${PATH}:" in
    *":$1:"*) return 0 ;;
    *) return 1 ;;
  esac
}

# same_file compares two paths to the binary, ignoring the extension.
#
# Windows needs this and nothing else does. The installed file is
# endurance.exe, and `command -v endurance` in git-bash answers with the path
# *without* the suffix — so a literal comparison says the file the installer
# just wrote and the file on PATH are two different endurances, and the run
# ends by warning about a conflict with itself.
same_file() { [ "${1%.exe}" = "${2%.exe}" ]; }

writable() { [ -d "$1" ] && [ -w "$1" ]; }

# choose_dir picks the install directory.
#
# The order is: what the user asked for, then a directory that is already on
# PATH and already writable, then the conventional user directory, creating it.
# /usr/local/bin is considered only when it is already writable — this script
# never runs sudo, because a one-line curl into a root-owned directory is a
# thing people should have to decide to do rather than have happen to them.
choose_dir() {
  if [ -n "${ENDURANCE_INSTALL_DIR:-}" ]; then
    echo "${ENDURANCE_INSTALL_DIR%/}"
    return
  fi
  local candidate
  for candidate in "$HOME/.local/bin" "$HOME/bin" /usr/local/bin; do
    if writable "$candidate" && on_path "$candidate"; then
      echo "$candidate"
      return
    fi
  done
  # Nothing usable is on PATH. Pick the conventional one and say so afterwards;
  # installing somewhere nothing will look is worse than one line of advice.
  echo "$HOME/.local/bin"
}

# shell_profile names the file the PATH line belongs in, so the advice is a
# thing to do rather than a thing to work out.
shell_profile() {
  case "${SHELL:-}" in
    */zsh) echo "$HOME/.zshrc" ;;
    */bash) echo "$HOME/.bashrc" ;;
    *) echo "$HOME/.profile" ;;
  esac
}

# ---------------------------------------------------------------------------
# Which version is already here
# ---------------------------------------------------------------------------

# version_of asks a binary what it is. `version --short` prints exactly
# "endurance vX.Y.Z" and reads no repository, which is what makes it safe to
# call on a machine that has never seen this project.
version_of() {
  local out
  out="$("$1" version --short 2>/dev/null || true)"
  printf '%s\n' "$out" | awk '/^endurance /{print $2; exit}'
}

# compare_versions prints -1, 0 or 1 for a<b, a==b, a>b.
#
# Numeric field-by-field rather than `sort -V`, which BSD sort does not
# reliably have. Anything it cannot parse compares as "different, not ordered",
# which the caller reports as a replacement rather than guessing a direction.
compare_versions() {
  local a="${1#v}" b="${2#v}" i x y
  if [ "$a" = "$b" ]; then
    echo 0
    return
  fi
  for i in 1 2 3; do
    x="$(printf '%s' "$a" | cut -d. -f"$i")"
    y="$(printf '%s' "$b" | cut -d. -f"$i")"
    case "$x$y" in
      *[!0-9]* | "")
        echo different
        return
        ;;
    esac
    if [ "$x" -lt "$y" ]; then
      echo -1
      return
    fi
    if [ "$x" -gt "$y" ]; then
      echo 1
      return
    fi
  done
  echo different
}

# ---------------------------------------------------------------------------
# The run
# ---------------------------------------------------------------------------

main() {
  local os arch asset target version url tmp dir installed digest expected
  local previous previous_path

  os="$(detect_os)"
  arch="$(detect_arch)"
  if [ -z "$os" ] || [ -z "$arch" ]; then
    die "Endurance does not publish a binary for $(uname -s)/$(uname -m)" \
      "published: windows/amd64 (git-bash), darwin/arm64, darwin/amd64, linux/amd64, linux/arm64" \
      "build it from source instead: go build -o endurance ./cli"
  fi

  asset="$(asset_name "$os" "$arch")"
  if [ -z "$asset" ]; then
    die "Endurance does not publish a binary for ${os}/${arch}" \
      "published: windows/amd64 (git-bash), darwin/arm64, darwin/amd64, linux/amd64, linux/arm64" \
      "build it from source instead: go build -o endurance ./cli"
  fi

  command -v curl >/dev/null 2>&1 ||
    die "curl is required to download the release" "install curl and re-run this line"

  version="${ENDURANCE_VERSION:-}"
  url="$(base_url "$version")"
  target="$(installed_name "$os")"

  say "Endurance CLI installer"
  if [ -n "$version" ]; then
    say "  release   ${version}"
  else
    say "  release   latest"
  fi
  say "  machine   ${os}/${arch}"
  say "  asset     ${asset}"
  say ""

  tmp="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT

  say "Downloading ${url}/${asset}"
  fetch "${url}/${asset}" "${tmp}/${target}" ||
    die "could not download ${url}/${asset}" \
      "check the release exists: ${RELEASES_URL}" \
      "or pin one: ENDURANCE_VERSION=v0.11.0 before the curl line"

  say "Downloading ${url}/${CHECKSUMS}"
  fetch "${url}/${CHECKSUMS}" "${tmp}/${CHECKSUMS}" ||
    die "the release published no ${CHECKSUMS}" \
      "nothing was installed — a binary that cannot be checked is not installed"

  digest="$(sha256_of "${tmp}/${target}")"
  [ -n "$digest" ] ||
    die "no sha256 tool on this machine (sha256sum, shasum or openssl)" \
      "nothing was installed — the download cannot be verified without one"

  expected="$(expected_sha "${tmp}/${CHECKSUMS}" "$asset")"
  [ -n "$expected" ] ||
    die "${CHECKSUMS} does not list ${asset}" \
      "nothing was installed"

  if [ "$digest" != "$expected" ]; then
    die "checksum mismatch for ${asset}" \
      "expected ${expected}" \
      "got      ${digest}" \
      "nothing was installed"
  fi
  say "  sha256    ${digest}  verified"
  say ""

  chmod +x "${tmp}/${target}"

  # Run it before installing it. A binary for the wrong architecture downloads
  # perfectly and checksums perfectly, and the first thing anybody would have
  # done with it is find out it cannot run — so this does that, here, where
  # nothing has been replaced yet.
  installed="$(version_of "${tmp}/${target}")"
  [ -n "$installed" ] ||
    die "the downloaded binary did not run on this machine" \
      "it was built for ${os}/${arch}; \`uname -sm\` says $(uname -sm)" \
      "nothing was installed"

  # What is already here, if anything.
  previous=""
  previous_path=""
  if command -v "$BINARY" >/dev/null 2>&1; then
    previous_path="$(command -v "$BINARY")"
    previous="$(version_of "$previous_path")"
  fi

  dir="$(choose_dir)"
  mkdir -p "$dir" || die "could not create ${dir}" "set ENDURANCE_INSTALL_DIR to somewhere writable"
  # One spelling of the directory for the rest of the run. On git-bash a user
  # can perfectly reasonably pass C:\Users\me\bin while $PATH holds
  # /c/Users/me/bin, and the two are the same directory that no string
  # comparison will match — which would end a correct install with a warning
  # that it is not on PATH, and a printed path that disagrees with itself two
  # lines later.
  dir="$(cd "$dir" && pwd)"
  writable "$dir" || die "${dir} is not writable" \
    "set ENDURANCE_INSTALL_DIR to somewhere you own — this installer does not use sudo"

  if [ -n "$previous" ]; then
    case "$(compare_versions "$previous" "$installed")" in
      -1) say "Upgrading ${previous} to ${installed}" ;;
      0) say "Reinstalling ${installed} — the same version is already here" ;;
      1) say "Downgrading ${previous} to ${installed} — this is older than what is installed" ;;
      *) say "Replacing ${previous} with ${installed}" ;;
    esac
    say "  replacing ${previous_path}"
  else
    say "Installing ${installed}"
  fi

  # Copy then remove rather than mv: /tmp is often a different filesystem, and
  # a cross-device mv fails on exactly the machines this has to work on.
  cp "${tmp}/${target}" "${dir}/${target}.new" ||
    die "could not write to ${dir}" "set ENDURANCE_INSTALL_DIR to somewhere writable"
  chmod +x "${dir}/${target}.new"
  mv -f "${dir}/${target}.new" "${dir}/${target}" ||
    die "could not replace ${dir}/${target}" \
      "if it is running, close it and re-run this line"

  # Prove it, from where it now lives. Saying "installed" and then finding out
  # is the fault this project keeps re-learning: never claim an outcome you did
  # not observe.
  local confirmed
  confirmed="$(version_of "${dir}/${target}")"
  if [ "$confirmed" != "$installed" ]; then
    die "installed ${dir}/${target}, but it reports '${confirmed:-nothing}' rather than ${installed}" \
      "the file is there and something is wrong with it — remove it and re-run"
  fi

  say ""
  say "endurance ${confirmed} is installed at ${dir}/${target}"

  if ! on_path "$dir"; then
    say ""
    warn "${dir} is not on PATH — nothing will find \`endurance\` yet"
    say "  add this line to $(shell_profile), then open a new shell:"
    say ""
    say "    export PATH=\"${dir}:\$PATH\""
  elif [ -n "$previous_path" ] && ! same_file "$previous_path" "${dir}/${target}"; then
    warn "another endurance is still on PATH at ${previous_path}"
    say "  it will be found first unless ${dir} comes earlier · \`endurance uninstall\` removes both"
  fi

  say ""
  say "Next:"
  say "  endurance doctor      checks Docker, kind, kubectl, helm, istioctl and git"
  say "  endurance init        stands the platform up and deploys an application"
  say ""
  say "  endurance uninstall   removes the binary again · it never touches a cluster"
}

# Sourced with ENDURANCE_LIB=1, this file defines its functions and installs
# nothing, which is how the unit tests reach detect_arch, asset_name,
# choose_dir and compare_versions without downloading anything.
if [ "${ENDURANCE_LIB:-}" != "1" ]; then
  main "$@"
fi
