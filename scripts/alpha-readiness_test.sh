#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

die() {
  printf 'alpha-readiness-test: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/belay-alpha-readiness-test.XXXXXX")"
cleanup() {
  rm -rf -- "${test_root}"
}
trap cleanup EXIT

fixture_root="${test_root}/repository"
stub_bin="${test_root}/bin"
test_log="${test_root}/invocations.log"
mkdir -p -- "${fixture_root}/scripts" "${stub_bin}"
cp -- "${script_dir}/alpha-readiness.sh" "${fixture_root}/scripts/alpha-readiness.sh"

cat > "${stub_bin}/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  -s) printf 'Darwin\n' ;;
  -m) printf 'arm64\n' ;;
  *) exit 2 ;;
esac
EOF

cat > "${stub_bin}/make" <<'EOF'
#!/usr/bin/env bash
printf 'make\n' >> "${ALPHA_READINESS_TEST_LOG}"
EOF

cat > "${stub_bin}/shasum" <<'EOF'
#!/usr/bin/env bash
printf 'archive: OK\n'
EOF

cat > "${fixture_root}/scripts/build-developer-preview.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
version=""
output_dir=""
while (($# > 0)); do
  case "$1" in
    --version)
      version="$2"
      shift 2
      ;;
    --output-dir)
      output_dir="$2"
      shift 2
      ;;
    --arch)
      shift 2
      ;;
    *)
      exit 2
      ;;
  esac
done
mkdir -p -- "${output_dir}"
archive="${output_dir}/belay-local-developer-alpha-v${version}-darwin-arm64.tar.gz"
: > "${archive}"
: > "${archive}.sha256"
printf 'build:%s\n' "${BELAY_ALLOW_DIRTY-unset}" >> "${ALPHA_READINESS_TEST_LOG}"
EOF

cat > "${fixture_root}/scripts/smoke-developer-preview.sh" <<'EOF'
#!/usr/bin/env bash
printf 'smoke\n' >> "${ALPHA_READINESS_TEST_LOG}"
EOF

chmod +x \
  "${stub_bin}/uname" \
  "${stub_bin}/make" \
  "${stub_bin}/shasum" \
  "${fixture_root}/scripts/build-developer-preview.sh" \
  "${fixture_root}/scripts/smoke-developer-preview.sh"

run_readiness() {
  mode="$1"
  output_dir="${test_root}/dist-${mode}"
  : > "${test_log}"
  if [[ "${mode}" == "validation" ]]; then
    BELAY_ALPHA_ALLOW_DIRTY=1 \
      ALPHA_READINESS_TEST_LOG="${test_log}" \
      PATH="${stub_bin}:/usr/bin:/bin" \
      /bin/bash "${fixture_root}/scripts/alpha-readiness.sh" \
      --output-dir "${output_dir}" >/dev/null 2>&1
  else
    unset BELAY_ALPHA_ALLOW_DIRTY
    ALPHA_READINESS_TEST_LOG="${test_log}" \
      PATH="${stub_bin}:/usr/bin:/bin" \
      /bin/bash "${fixture_root}/scripts/alpha-readiness.sh" \
      --output-dir "${output_dir}" >/dev/null 2>&1
  fi
}

run_readiness clean
grep -Fxq 'build:unset' "${test_log}" ||
  die "clean build inherited BELAY_ALLOW_DIRTY or did not execute"
grep -Fxq 'make' "${test_log}" || die "clean path skipped verification"
grep -Fxq 'smoke' "${test_log}" || die "clean path skipped smoke"

run_readiness validation
grep -Fxq 'build:1' "${test_log}" ||
  die "validation build did not set BELAY_ALLOW_DIRTY=1"

printf 'alpha-readiness Bash compatibility test passed\n'
