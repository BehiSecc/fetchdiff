#!/usr/bin/env bash

set -Eeuo pipefail

readonly module="github.com/BehiSecc/fetchdiff/cmd/fetchdiff"
readonly destination="/usr/local/bin/fetchdiff"
stage=""

cleanup() {
  if [[ -n "$stage" ]]; then
    sudo rm -f -- "$stage" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

fail() {
  printf 'FetchDiff installer: %s\n' "$1" >&2
  exit 1
}

if [[ ${EUID:-$(id -u)} -eq 0 ]]; then
  if [[ -n ${SUDO_USER:-} ]]; then
    fail "do not pipe the installer through sudo; run it as ${SUDO_USER} instead"
  fi
  fail "run this installer as a non-root user; it will request sudo only when needed"
fi

[[ $(uname -s) == "Linux" ]] || fail "automatic service installation currently requires Linux"
command -v go >/dev/null 2>&1 || fail "Go is required. Install it from https://go.dev/doc/install and run this installer again"
command -v sudo >/dev/null 2>&1 || fail "sudo is required to install the binary and systemd unit"
command -v systemctl >/dev/null 2>&1 || fail "systemd is required for automatic service installation"
[[ -n ${HOME:-} && $HOME == /* && -d $HOME && -O $HOME ]] || fail "HOME must be an existing directory owned by the current user"

install_user=$(id -un) || fail "could not determine the current user"
version=${FETCHDIFF_VERSION:-latest}

printf 'Installing FetchDiff with Go...\n'
go install "${module}@${version}" || fail "go install failed"

go_bin=$(go env GOBIN) || fail "could not read GOBIN"
if [[ -z $go_bin ]]; then
  go_path=$(go env GOPATH) || fail "could not read GOPATH"
  go_bin=${go_path%%:*}/bin
fi
source_binary="${go_bin%/}/fetchdiff"
[[ $source_binary == /* ]] || fail "Go binary directory is not an absolute path: ${go_bin}"
[[ -f $source_binary && -x $source_binary ]] || fail "installed binary was not found at ${source_binary}"

printf 'Installing %s (sudo is required)...\n' "$destination"
sudo -v || fail "sudo authentication failed"
stage="/usr/local/bin/.fetchdiff.install.$$"
sudo install -o root -g root -m 0755 -- "$source_binary" "$stage" || fail "could not stage the FetchDiff binary"
sudo mv -f -- "$stage" "$destination" || fail "could not install FetchDiff in /usr/local/bin"
stage=""

"$destination" --version >/dev/null || fail "the installed FetchDiff binary did not run successfully"
"$destination" init || fail "could not initialize ${HOME}/.fetchdiff"
sudo "$destination" service install --user "$install_user" || fail "could not install the systemd unit"

cat <<EOF

✓ FetchDiff is installed.

Next:
  1. Add a target:
     fetchdiff add https://example.com/app.js --name app-js --every 24h
  2. Edit ${HOME}/.fetchdiff/providers.yaml (or copy your Notify provider config there).
  3. Test notifications:
     fetchdiff notify-test
  4. Start FetchDiff at boot:
     sudo systemctl enable --now fetchdiff

If FetchDiff was already running, restart it with:
  sudo systemctl restart fetchdiff
EOF
