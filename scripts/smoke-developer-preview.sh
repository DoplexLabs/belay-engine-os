#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly NUMBAT_VERSION_MARKER="b5172bb8bb8f"

die() {
  printf 'smoke-developer-preview: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

if (($# != 1)); then
  die "usage: scripts/smoke-developer-preview.sh PATH_TO_ARCHIVE.tar.gz"
fi

[[ "$(uname -s)" == "Darwin" ]] || die "runtime smoke tests require macOS"
for required in \
  basename chmod cut dirname env file find grep mkdir mktemp pwd rm shasum \
  sort tar uname; do
  require_command "${required}"
done

archive="$1"
[[ -f "${archive}" ]] || die "archive not found: ${archive}"
archive="$(cd -- "$(dirname -- "${archive}")" && pwd -P)/$(basename -- "${archive}")"

smoke_tmp="$(mktemp -d "${TMPDIR:-/tmp}/belay-preview-smoke.XXXXXX")"
cleanup() {
  rm -rf -- "${smoke_tmp}"
}
trap cleanup EXIT

listing="${smoke_tmp}/archive-contents.txt"
tar -tzf "${archive}" > "${listing}"
while IFS= read -r entry; do
  case "${entry}" in
    ""|/*|..|../*|*/../*)
      die "unsafe archive path: ${entry}"
      ;;
  esac
done < "${listing}"

top_levels="$(cut -d/ -f1 "${listing}" | LC_ALL=C sort -u)"
[[ "$(printf '%s\n' "${top_levels}" | grep -c .)" == "1" ]] ||
  die "archive must contain exactly one top-level directory"
package_name="${top_levels}"

required_entries=(
  "${package_name}/bin/belay"
  "${package_name}/bin/numbat"
  "${package_name}/LICENSE"
  "${package_name}/licenses/numbat/LICENSE"
  "${package_name}/licenses/numbat/THIRD_PARTY_LICENSES.txt"
  "${package_name}/README.md"
  "${package_name}/llms.txt"
  "${package_name}/docs/developer-alpha.md"
  "${package_name}/docs/clean-machine-alpha-qa.md"
  "${package_name}/BUILD-INFO.txt"
  "${package_name}/SHA256SUMS"
)
for required_entry in "${required_entries[@]}"; do
  grep -Fxq "${required_entry}" "${listing}" ||
    die "archive is missing ${required_entry}"
done

tar -xzf "${archive}" -C "${smoke_tmp}"
package_root="${smoke_tmp}/${package_name}"
(
  cd -- "${package_root}"
  shasum -a 256 -c SHA256SUMS
)

belay="${package_root}/bin/belay"
numbat="${package_root}/bin/numbat"
[[ -x "${belay}" && -x "${numbat}" ]] || die "packaged binaries are not executable"

machine="$(uname -m)"
belay_file="$(file "${belay}")"
case "${machine}" in
  arm64)
    [[ "${belay_file}" == *"arm64"* ]] || die "archive is not native arm64"
    ;;
  x86_64)
    [[ "${belay_file}" == *"x86_64"* ]] || die "archive is not native amd64"
    ;;
  *)
    die "unsupported smoke-test architecture: ${machine}"
    ;;
esac

sandbox_profile='(version 1)(allow default)(deny network*)'
sandbox_available="false"
if command -v sandbox-exec >/dev/null 2>&1; then
  if sandbox-exec -p "${sandbox_profile}" /usr/bin/true >/dev/null 2>&1; then
    sandbox_available="true"
  else
    printf '%s\n' \
      "smoke-developer-preview: warning: sandbox-exec is present but unavailable; continuing without nested sandboxing" \
      >&2
  fi
fi

run_without_network() {
  if [[ "${sandbox_available}" == "true" ]]; then
    sandbox-exec -p "${sandbox_profile}" "$@"
  else
    "$@"
  fi
}

run_without_network "${numbat}" version | grep -Fq "${NUMBAT_VERSION_MARKER}" ||
  die "packaged Numbat version marker mismatch"
run_without_network "${belay}" help | grep -Fq "quickstart" ||
  die "packaged Belay does not advertise quickstart"

# This initializes only disposable user and Belay homes and performs read-only
# agent inventory through the same runtime preparation used by quickstart. It
# proves the embedded pin resolves and verifies sibling bin/numbat without
# manual pin flags. It never sees the real user home, invokes hook installation,
# opens the Local database or Keychain, or opens a browser.
smoke_user_home="${smoke_tmp}/user-home"
smoke_home="${smoke_tmp}/belay-home"
mkdir -p -- "${smoke_user_home}"
chmod 0700 "${smoke_user_home}"
run_without_network env HOME="${smoke_user_home}" "${belay}" agents \
  --home "${smoke_home}" \
  >/dev/null

[[ -f "${smoke_home}/config.json" ]] || die "safe first-run config was not created"
numbat_sha256="$(shasum -a 256 "${numbat}" | cut -d ' ' -f1)"
grep -Fq "\"numbat_sha256\": \"${numbat_sha256}\"" \
  "${smoke_home}/config.json" ||
  die "safe first-run config did not materialize the embedded checksum"
grep -Fq "\"numbat_version_marker\": \"${NUMBAT_VERSION_MARKER}\"" \
  "${smoke_home}/config.json" ||
  die "safe first-run config did not materialize the embedded version marker"
materialized_numbat="$(find "${smoke_home}/bin" -type f -name 'numbat-*' -print)"
[[ "$(printf '%s\n' "${materialized_numbat}" | grep -c .)" == "1" ]] ||
  die "safe first-run did not materialize exactly one pinned Numbat binary"
materialized_name="$(basename -- "${materialized_numbat}")"
grep -Fq "/bin/${materialized_name}\"" "${smoke_home}/config.json" ||
  die "safe first-run config did not record materialized packaged sibling Numbat"
[[ "$(shasum -a 256 "${materialized_numbat}" | cut -d ' ' -f1)" == "${numbat_sha256}" ]] ||
  die "materialized Numbat checksum differs from packaged sibling"
run_without_network "${materialized_numbat}" version |
  grep -Fq "${NUMBAT_VERSION_MARKER}" ||
  die "materialized Numbat version marker mismatch"
[[ ! -e "${smoke_home}/live/codex.ndjson" && ! -e "${smoke_home}/live/claude.ndjson" ]] ||
  die "smoke test unexpectedly created live-hook spool files"
[[ -z "$(find "${smoke_user_home}" -mindepth 1 -print -quit)" ]] ||
  die "Numbat inventory unexpectedly wrote to the disposable user home"

printf 'developer-preview smoke test passed: %s\n' "${archive}"
