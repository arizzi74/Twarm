#!/bin/sh
# This HTTPS-fetched script is the initial trust root. Before execution, verify
# the known v0.7.32 native manager against a checksum pinned in this script.
set -eu

# The native manager selects gateway setup under sudo, worker setup otherwise.
# Explicit administration commands continue to be forwarded unchanged.
if [ "$#" -eq 0 ]; then
  set -- setup
fi

fail() { printf '%s\n' "Installation failed: $*" >&2; exit 1; }
stable_tag() {
  case "$1" in v*) tag_numbers=${1#v} ;; *) return 1 ;; esac
  tag_major=${tag_numbers%%.*}; tag_rest=${tag_numbers#*.}
  [ "$tag_rest" != "$tag_numbers" ] || return 1
  tag_minor=${tag_rest%%.*}; tag_patch=${tag_rest#*.}
  [ "$tag_patch" != "$tag_rest" ] || return 1
  for tag_part in "$tag_major" "$tag_minor" "$tag_patch"; do
    case "$tag_part" in ''|*[!0-9]*|0[0-9]*) return 1 ;; esac
  done
}
select_version() {
  stable_tag "$1" || fail '--version must select a stable vMAJOR.MINOR.PATCH release'
  [ -z "$release_tag" ] || [ "$release_tag" = "$1" ] || fail 'conflicting --version arguments'
  release_tag=$1
}
select_repo() {
  case "$1" in ''|*[!A-Za-z0-9_./-]*|/*|*/|*/*/*) fail '--repo must be OWNER/REPOSITORY' ;; esac
  case "$1" in */*) ;; *) fail '--repo must be OWNER/REPOSITORY' ;; esac
  for repo_part in "${1%%/*}" "${1#*/}"; do
    case "$repo_part" in .|..) fail '--repo must be OWNER/REPOSITORY' ;; esac
  done
  [ -z "$requested_repo" ] || [ "$requested_repo" = "$1" ] || fail 'conflicting --repo arguments'
  requested_repo=$1
  repository=$1
}

repository=arizzi74/Twarm
requested_repo=
release_tag=
expect=
for argument do
  if [ -n "$expect" ]; then
    case "$expect" in version) select_version "$argument" ;; repo) select_repo "$argument" ;; esac
    expect=
    continue
  fi
  case "$argument" in
    --version) expect=version ;;
    --version=*) select_version "${argument#--version=}" ;;
    --repo) expect=repo ;;
    --repo=*) select_repo "${argument#--repo=}" ;;
  esac
done
[ -z "$expect" ] || fail "--$expect requires a value"
for utility in curl uname mktemp chmod mv rm; do
  command -v "$utility" >/dev/null 2>&1 || fail "$utility is required"
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  fail 'sha256sum or shasum is required'
fi
case "$(uname -s)" in Linux) platform=linux ;; Darwin) platform=darwin ;; *) fail 'unsupported operating system' ;; esac
case "$(uname -m)" in x86_64|amd64) architecture=amd64 ;; aarch64|arm64) architecture=arm64 ;; *) fail 'unsupported CPU architecture' ;; esac
asset="codex-telegramgw-$platform-$architecture"
# These pins come from the authenticated MACT-signed v0.7.32 SHA256SUMS manifest
# (SHA256 4957ea536a64fe92b8bd67cbcdd939a4b1f0452f7708955c0ad58f06845c82fd).
# The bootstrap version is independent of the release/repository being installed.
# This manager verifies signed provenance and checksums for the target release.
bootstrap_version=v0.7.32
case "$platform-$architecture" in
  linux-amd64) expected=59107b15918bb155ed4bae2a10c9e5d6890a63d73d2ade7b1ef58d53fd7868ae ;;
  linux-arm64) expected=edf09680c6d231b54455f43fd0ecb6bb3caf47ac9278c01d7a07c501921beb4c ;;
  darwin-amd64) expected=651298e4197a147427b42ed82dda0299dcc994efbf9a7979462004bbb5465149 ;;
  darwin-arm64) expected=e277bda2339f04886932307fe9c79d626673bdce2857f30091a421ac3ac64a50 ;;
esac
printf 'Preparing the %s installer for %s/%s...\n' "$1" "$platform" "$architecture"
download() {
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --tlsv1.2 --connect-timeout 15 --max-time 180 --retry 2 "$@"
}
selection=latest
if [ -n "$release_tag" ]; then selection="tag/$release_tag"; fi
release_url=$(download --max-filesize 1048576 --output /dev/null --write-out '%{url_effective}' \
  "https://github.com/$repository/releases/$selection") || fail 'could not resolve the selected release'
# Only the deliberate official rename can change repository scope. Resolve tag
# pages too: historical release assets acquire the renamed repository's URLs.
case "$repository:$release_url" in
  arizzi74/Codex-Telegram-Gateway:https://github.com/arizzi74/MACT/releases/tag/*) repository=arizzi74/MACT ;;
  arizzi74/Codex-Telegram-Gateway:https://github.com/arizzi74/Twarm/releases/tag/*|arizzi74/MACT:https://github.com/arizzi74/Twarm/releases/tag/*) repository=arizzi74/Twarm ;;
esac
case "$release_url" in
  "https://github.com/$repository/releases/tag/"*) resolved_tag=${release_url##*/} ;;
  *) fail 'GitHub returned an unexpected release URL' ;;
esac
stable_tag "$resolved_tag" || fail 'GitHub did not return a stable release tag'
[ "$release_url" = "https://github.com/$repository/releases/tag/$resolved_tag" ] || fail 'GitHub returned an unexpected release URL'
[ -z "$release_tag" ] || [ "$release_tag" = "$resolved_tag" ] || fail 'GitHub returned the wrong release version'
release_tag=$resolved_tag
case "$repository" in
  arizzi74/Twarm|arizzi74/MACT|arizzi74/Codex-Telegram-Gateway)
    case "$release_tag" in
      v0.[0-6].*|v0.7.[0-9]|v0.7.[12][0-9]|v0.7.3[01])
        fail 'releases before v0.7.32 cannot follow the Twarm repository rename; install v0.7.32 or newer' ;;
    esac ;;
esac

temporary=
fallback=
cleanup() {
  [ -z "$temporary" ] || rm -rf -- "$temporary"
  [ -z "$fallback" ] || rm -rf -- "$fallback"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
temporary=$(mktemp -d "${TMPDIR:-/tmp}/codex-telegramgw-install.XXXXXXXX") || fail 'could not create a private temporary directory; set TMPDIR to a writable directory and retry'
base="https://github.com/arizzi74/Twarm/releases/download/$bootstrap_version"
printf 'Downloading and verifying installer %s for release %s...\n' "$bootstrap_version" "$release_tag"
download --max-filesize 67108864 --output "$temporary/$asset" "$base/$asset" || fail 'could not download the release manager'
printf '%s  %s\n' "$expected" "$asset" > "$temporary/manager.sha256"
verify_installer() {
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$temporary" && sha256sum -c manager.sha256) >/dev/null 2>&1 || fail 'installer checksum verification failed; no installer code was executed'
  else
    (cd "$temporary" && shasum -a 256 -c manager.sha256) >/dev/null 2>&1 || fail 'installer checksum verification failed; no installer code was executed'
  fi
}
verify_installer
chmod 0700 "$temporary/$asset"
# chmod cannot override a noexec mount. Probe only the verified manager's
# side-effect-free version command, so setup itself is never retried.
if "$temporary/$asset" version >/dev/null 2>&1; then
  :
else
  startup_status=$?
  [ "$startup_status" -eq 126 ] || fail "verified installer startup failed (exit $startup_status)"
  case "${HOME:-}" in
    /*) [ "$HOME" != / ] || fail 'the temporary directory blocks execution; set TMPDIR to a writable directory that permits execution and retry' ;;
    *) fail 'the temporary directory blocks execution and HOME is unavailable; set TMPDIR to a writable directory that permits execution and retry' ;;
  esac
  printf 'The temporary directory blocks execution; trying a private directory in your home...\n'
  fallback=$(mktemp -d "${HOME%/}/.mact-install.XXXXXXXX") || fail 'could not create a private directory in HOME; set TMPDIR to a writable directory that permits execution and retry'
  mv "$temporary/$asset" "$temporary/manager.sha256" "$fallback/" || fail 'could not relocate the verified installer'
  rm -rf -- "$temporary"
  temporary=$fallback
  fallback=
  verify_installer
  # Native setup also executes downloaded packages and runtime preflights from
  # its temporary directories. Keep those on the same executable filesystem.
  TMPDIR=$temporary
  export TMPDIR
  "$temporary/$asset" version >/dev/null 2>&1 || fail 'cannot execute the verified installer from HOME; execution may be blocked by mount options or a security policy. Set TMPDIR to a writable directory that permits execution and retry'
fi
CODEX_TELEGRAMGW_BOOTSTRAP_RELEASE=$release_tag
export CODEX_TELEGRAMGW_BOOTSTRAP_RELEASE
printf 'Pinned installer checksum verified. Follow the terminal prompts; press Enter to accept defaults.\n'
"$temporary/$asset" "$@"
