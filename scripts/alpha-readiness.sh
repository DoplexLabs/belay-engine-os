#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly DEFAULT_VERSION="0.0.1-alpha.8"

usage() {
  cat <<'EOF'
usage: scripts/alpha-readiness.sh [options]

Run non-publishing Apple Silicon Developer Alpha readiness checks.

Options:
  --version VERSION    Alpha version label (default: 0.0.1-alpha.8)
  --output-dir PATH    Artifact output directory (default: ./dist)
  -h, --help           Show this help

The script runs verification, builds one darwin/arm64 archive, verifies its
checksum, runs the packaged smoke test, and prints remaining manual gates.
It does not sign, notarize, tag, publish, release, deploy, or modify user state.

By default the packaging step requires a clean checkout. For local validation
of uncommitted release-surface work only, set BELAY_ALPHA_ALLOW_DIRTY=1. Such an
artifact records belay_dirty=true and must not be distributed.
EOF
}

die() {
  printf 'alpha-readiness: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repository_root="$(cd -- "${script_dir}/.." && pwd -P)"
alpha_version="${BELAY_ALPHA_VERSION:-${DEFAULT_VERSION}}"
output_dir="${repository_root}/dist"

while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || die "--version requires a value"
      alpha_version="$2"
      shift 2
      ;;
    --output-dir)
      (($# >= 2)) || die "--output-dir requires a value"
      output_dir="$2"
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

for required in bash make shasum uname; do
  require_command "${required}"
done

[[ "$(uname -s)" == "Darwin" ]] || die "alpha readiness requires macOS"
[[ "$(uname -m)" == "arm64" ]] ||
  die "the current Developer Alpha readiness gate is Apple Silicon only"
[[ "${alpha_version}" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]] ||
  die "version may contain only letters, numbers, dots, underscores, and hyphens"

printf '==> verifying source and release surface\n'
make -C "${repository_root}" verify

validation_only="false"
if [[ "${BELAY_ALPHA_ALLOW_DIRTY:-0}" == "1" ]]; then
  validation_only="true"
  printf '%s\n' \
    "alpha-readiness: warning: building a dirty validation artifact; do not distribute it" \
    >&2
fi

printf '==> building unsigned darwin/arm64 alpha archive\n'
if [[ "${validation_only}" == "true" ]]; then
  BELAY_ALLOW_DIRTY=1 \
    "${repository_root}/scripts/build-developer-preview.sh" \
    --arch arm64 \
    --version "${alpha_version}" \
    --output-dir "${output_dir}"
else
  "${repository_root}/scripts/build-developer-preview.sh" \
    --arch arm64 \
    --version "${alpha_version}" \
    --output-dir "${output_dir}"
fi

archive_name="belay-local-developer-alpha-v${alpha_version}-darwin-arm64.tar.gz"
archive="${output_dir}/${archive_name}"
checksum="${archive}.sha256"
[[ -f "${archive}" && -f "${checksum}" ]] ||
  die "expected archive or checksum was not created"

printf '==> verifying archive checksum\n'
(
  cd -- "${output_dir}"
  shasum -a 256 -c "${archive_name}.sha256"
)

printf '==> running packaged smoke test\n'
"${repository_root}/scripts/smoke-developer-preview.sh" "${archive}"

if [[ "${validation_only}" == "true" ]]; then
  cat <<EOF

Automated local validation checks passed.

Artifact:
  ${archive}

NOT DISTRIBUTABLE: BELAY_ALPHA_ALLOW_DIRTY=1 was set. The artifact must record
belay_dirty=true and is never a Developer Alpha release candidate. Re-run from
a clean checkout without BELAY_ALPHA_ALLOW_DIRTY before completing release QA.

This script did not sign, notarize, tag, publish, release, deploy, or contact a
paid service.
EOF
  exit 0
fi

cat <<EOF

Automated Apple Silicon alpha checks passed.

Artifact:
  ${archive}

Manual gates still required before distribution:
  [ ] BUILD-INFO.txt records belay_dirty=false.
  [ ] Repository/artifact access is authorized for intended testers.
  [ ] Name clearance is recorded.
  [ ] Clean-machine checklist is completed:
      docs/launch/clean-machine-alpha-qa.md
  [ ] External Codex and Claude Code testers meet the install-to-insight gate.
  [ ] Offline browser/MCP and privacy-canary evidence is attached.
  [ ] A human explicitly authorizes any publication or release action.

This script did not sign, notarize, tag, publish, release, deploy, or contact a
paid service.
EOF
