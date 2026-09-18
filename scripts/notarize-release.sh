#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

die() {
  printf 'notarize-release: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
usage: notarize-release.sh --archive PATH [--keychain-profile PROFILE]

Submit a signed macOS Belay archive to Apple, wait for acceptance, then mark
and repackage the archive as notarized.

Without --keychain-profile, these environment variables are required:
  APPLE_ID
  APPLE_TEAM_ID
  APPLE_APP_SPECIFIC_PASSWORD
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

metadata_value() {
  local path="$1"
  local key="$2"
  awk -F= -v key="${key}" '
    $1 == key {
      sub(/^[^=]*=/, "")
      print
      found++
    }
    END {
      if (found != 1) {
        exit 1
      }
    }
  ' "${path}"
}

archive=""
keychain_profile=""
while (($# > 0)); do
  case "$1" in
    --archive)
      (($# >= 2)) || die "--archive requires a value"
      archive="$2"
      shift 2
      ;;
    --keychain-profile)
      (($# >= 2)) || die "--keychain-profile requires a value"
      keychain_profile="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[[ -n "${archive}" && -f "${archive}" ]] || die "--archive must name an existing file"
archive="$(cd -- "$(dirname -- "${archive}")" && pwd -P)/$(basename -- "${archive}")"

for required in awk codesign ditto find go grep mktemp mv rm shasum sort tar xcrun; do
  require_command "${required}"
done

if [[ -z "${keychain_profile}" ]]; then
  [[ -n "${APPLE_ID:-}" ]] || die "APPLE_ID is required"
  [[ -n "${APPLE_TEAM_ID:-}" ]] || die "APPLE_TEAM_ID is required"
  [[ -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ]] ||
    die "APPLE_APP_SPECIFIC_PASSWORD is required"
fi

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
notary_tmp="$(mktemp -d "${TMPDIR:-/tmp}/belay-notary.XXXXXX")"
cleanup() {
  rm -rf -- "${notary_tmp}"
}
trap cleanup EXIT

listing="${notary_tmp}/archive-contents.txt"
tar -tzf "${archive}" > "${listing}"
top_levels="$(cut -d/ -f1 "${listing}" | LC_ALL=C sort -u)"
[[ "$(printf '%s\n' "${top_levels}" | grep -c .)" == "1" ]] ||
  die "archive must contain exactly one top-level directory"
tar -xzf "${archive}" -C "${notary_tmp}"
package_root="${notary_tmp}/${top_levels}"
build_info="${package_root}/BUILD-INFO.txt"

[[ "$(metadata_value "${build_info}" signed)" == "true" ]] ||
  die "archive must be Developer ID signed before notarization"
[[ "$(metadata_value "${build_info}" notarized)" == "false" ]] ||
  die "archive is already marked notarized"
codesign --verify --strict --verbose=2 "${package_root}/bin/belay"
codesign --verify --strict --verbose=2 "${package_root}/bin/numbat"

submission="${notary_tmp}/belay-notarization.zip"
ditto -c -k --keepParent "${package_root}" "${submission}"
result="${notary_tmp}/notary-result.json"
if [[ -n "${keychain_profile}" ]]; then
  xcrun notarytool submit "${submission}" \
    --keychain-profile "${keychain_profile}" \
    --wait \
    --output-format json > "${result}"
else
  xcrun notarytool submit "${submission}" \
    --apple-id "${APPLE_ID}" \
    --team-id "${APPLE_TEAM_ID}" \
    --password "${APPLE_APP_SPECIFIC_PASSWORD}" \
    --wait \
    --output-format json > "${result}"
fi
grep -Eq '"status"[[:space:]]*:[[:space:]]*"Accepted"' "${result}" ||
  die "Apple did not accept the notarization submission"

updated_info="${notary_tmp}/BUILD-INFO.txt"
awk '
  $0 == "notarized=false" {
    print "notarized=true"
    updated++
    next
  }
  { print }
  END {
    if (updated != 1) {
      exit 1
    }
  }
' "${build_info}" > "${updated_info}"
mv -- "${updated_info}" "${build_info}"

(
  cd -- "${package_root}"
  find . -type f ! -name SHA256SUMS -print |
    LC_ALL=C sort |
    while IFS= read -r path; do
      shasum -a 256 "${path#./}"
    done > SHA256SUMS
)

source_date_epoch="$(metadata_value "${build_info}" source_date_epoch)"
[[ "${source_date_epoch}" =~ ^[0-9]+$ ]] ||
  die "BUILD-INFO.txt contains an invalid source_date_epoch"
repacked="${notary_tmp}/$(basename -- "${archive}")"
go run "${repository_root}/scripts/package-preview.go" \
  -source "${package_root}" \
  -output "${repacked}" \
  -epoch "${source_date_epoch}"
mv -- "${repacked}" "${archive}"
(
  cd -- "$(dirname -- "${archive}")"
  shasum -a 256 "$(basename -- "${archive}")" > "$(basename -- "${archive}").sha256"
)

printf 'Apple notarization accepted: %s\n' "${archive}"
