#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly NUMBAT_REPOSITORY="https://github.com/DoplexLabs/numbat.git"
readonly NUMBAT_COMMIT="b5172bb8bb8f1d68edc4f3b9462de7e248dc5243"
readonly NUMBAT_VERSION_MARKER="b5172bb8bb8f"
readonly NUMBAT_LICENSE_SHA256="c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"
readonly NUMBAT_THIRD_PARTY_SHA256="c2732acc87437d691ad1c5fc70cd1c6596a8999a1926721a2004f3e7b7692c77"
readonly DEFAULT_VERSION="0.0.1-alpha.11"

usage() {
  cat <<'EOF'
usage: scripts/build-developer-preview.sh [options]

Build Belay Local Developer Alpha archives without publishing them.

Options:
  --os darwin|windows     Target operating system (default: darwin)
  --arch arm64|amd64|all  Target architecture (default: native for darwin,
                          amd64 for windows)
  --version VERSION       Artifact version label (default: 0.0.1-alpha.11)
  --output-dir PATH       Output directory (default: ./dist)
  --numbat-source PATH    Use an existing pristine Numbat checkout
  --codesign-identity ID  Sign both binaries with an Apple Developer ID
                          (darwin only)
  -h, --help              Show this help

macOS archives are tar.gz files and must be built on macOS. Windows archives
are zip files, are never signed by this script, and may be cross-compiled from
macOS or Linux. Windows packages are an engineering preview, not a release.

The build fails if the Belay tree is dirty unless BELAY_ALLOW_DIRTY=1 is set.
The Numbat checkout must always be clean and exactly at the approved commit.
EOF
}

die() {
  printf 'build-developer-preview: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repository_root="$(cd -- "${script_dir}/.." && pwd -P)"
target_os="darwin"
target_arch="native"
preview_version="${BELAY_PREVIEW_VERSION:-${DEFAULT_VERSION}}"
output_dir="${repository_root}/dist"
numbat_source=""
codesign_identity=""

while (($# > 0)); do
  case "$1" in
    --os)
      (($# >= 2)) || die "--os requires a value"
      target_os="$2"
      shift 2
      ;;
    --arch)
      (($# >= 2)) || die "--arch requires a value"
      target_arch="$2"
      shift 2
      ;;
    --version)
      (($# >= 2)) || die "--version requires a value"
      preview_version="$2"
      shift 2
      ;;
    --output-dir)
      (($# >= 2)) || die "--output-dir requires a value"
      output_dir="$2"
      shift 2
      ;;
    --numbat-source)
      (($# >= 2)) || die "--numbat-source requires a value"
      numbat_source="$2"
      shift 2
      ;;
    --codesign-identity)
      (($# >= 2)) || die "--codesign-identity requires a value"
      codesign_identity="$2"
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

case "${target_os}" in
  darwin)
    [[ "$(uname -s)" == "Darwin" ]] ||
      die "macOS developer-preview packaging is supported only on macOS"
    executable_suffix=""
    archive_format="tar.gz"
    ;;
  windows)
    [[ "$(uname -s)" == "Darwin" || "$(uname -s)" == "Linux" ]] ||
      die "Windows developer-preview packaging is supported only from macOS or Linux"
    [[ -z "${codesign_identity}" ]] ||
      die "--codesign-identity applies only to darwin targets"
    executable_suffix=".exe"
    archive_format="zip"
    ;;
  *)
    die "--os must be darwin or windows"
    ;;
esac
[[ "${preview_version}" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]] ||
  die "version may contain only letters, numbers, dots, underscores, and hyphens"

for required in \
  awk basename cat cmp dirname env find git go install mkdir mktemp pwd rm \
  shasum sort uname; do
  require_command "${required}"
done
if [[ -n "${codesign_identity}" ]]; then
  require_command codesign
fi

git -C "${repository_root}" rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
  die "Belay source must be a Git checkout"
belay_commit="$(git -C "${repository_root}" rev-parse HEAD)"
belay_dirty="false"
if [[ -n "$(git -C "${repository_root}" status --porcelain --untracked-files=all)" ]]; then
  belay_dirty="true"
  [[ "${BELAY_ALLOW_DIRTY:-0}" == "1" ]] ||
    die "Belay source tree is dirty; commit or clean it before packaging"
fi

case "${target_arch}" in
  native)
    if [[ "${target_os}" == "windows" ]]; then
      architectures=("amd64")
    else
      case "$(uname -m)" in
        arm64) architectures=("arm64") ;;
        x86_64) architectures=("amd64") ;;
        *) die "unsupported native architecture: $(uname -m)" ;;
      esac
    fi
    ;;
  arm64|amd64)
    architectures=("${target_arch}")
    ;;
  all)
    architectures=("arm64" "amd64")
    ;;
  *)
    die "--arch must be arm64, amd64, or all"
    ;;
esac

preview_tmp="$(mktemp -d "${TMPDIR:-/tmp}/belay-preview.XXXXXX")"
cleanup() {
  rm -rf -- "${preview_tmp}"
}
trap cleanup EXIT

if [[ -z "${numbat_source}" ]]; then
  numbat_source="${preview_tmp}/numbat"
  git init --quiet "${numbat_source}"
  git -C "${numbat_source}" remote add origin "${NUMBAT_REPOSITORY}"
  git -C "${numbat_source}" fetch --quiet --depth=1 origin "${NUMBAT_COMMIT}"
  git -C "${numbat_source}" checkout --quiet --detach FETCH_HEAD
else
  numbat_source="$(cd -- "${numbat_source}" && pwd -P)"
fi

git -C "${numbat_source}" rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
  die "Numbat source must be a Git checkout"
actual_numbat_commit="$(git -C "${numbat_source}" rev-parse HEAD)"
[[ "${actual_numbat_commit}" == "${NUMBAT_COMMIT}" ]] ||
  die "wrong Numbat commit: got ${actual_numbat_commit}, want ${NUMBAT_COMMIT}"
[[ -z "$(git -C "${numbat_source}" status --porcelain --untracked-files=all)" ]] ||
  die "Numbat source tree is dirty"

[[ "$(shasum -a 256 "${numbat_source}/LICENSE" | awk '{print $1}')" == "${NUMBAT_LICENSE_SHA256}" ]] ||
  die "Numbat LICENSE does not match the approved commit"
[[ "$(shasum -a 256 "${numbat_source}/THIRD_PARTY_LICENSES.txt" | awk '{print $1}')" == "${NUMBAT_THIRD_PARTY_SHA256}" ]] ||
  die "Numbat third-party attribution does not match the approved commit"
cmp -s "${numbat_source}/LICENSE" "${repository_root}/licenses/numbat/LICENSE" ||
  die "vendored Numbat LICENSE differs from the approved source"
cmp -s "${numbat_source}/THIRD_PARTY_LICENSES.txt" "${repository_root}/licenses/numbat/THIRD_PARTY_LICENSES.txt" ||
  die "vendored Numbat third-party attribution differs from the approved source"

source_date_epoch="${SOURCE_DATE_EPOCH:-$(git -C "${repository_root}" show -s --format=%ct "${belay_commit}")}"
[[ "${source_date_epoch}" =~ ^[0-9]+$ ]] || die "SOURCE_DATE_EPOCH must be an integer"

mkdir -p -- "${output_dir}"
output_dir="$(cd -- "${output_dir}" && pwd -P)"

for architecture in "${architectures[@]}"; do
  package_name="belay-local-developer-alpha-v${preview_version}-${target_os}-${architecture}"
  numbat_binary="${package_name}/bin/numbat${executable_suffix}"
  belay_binary="${package_name}/bin/belay${executable_suffix}"
  package_root="${preview_tmp}/${package_name}"
  mkdir -p -- "${package_root}/bin" "${package_root}/licenses/numbat" "${package_root}/docs"

  # The approved commit predates cel-go's repository move. This command-scoped
  # redirect resolves the same module without modifying the pristine checkout.
  env \
    CGO_ENABLED=0 \
    GOFLAGS= \
    GOOS="${target_os}" \
    GOARCH="${architecture}" \
    GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0="url.https://github.com/cel-expr/cel-go.insteadOf" \
    GIT_CONFIG_VALUE_0="https://github.com/google/cel-go" \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_CONFIG_SYSTEM=/dev/null \
    go -C "${numbat_source}" build \
      -trimpath \
      -ldflags="-s -w" \
      -o "${preview_tmp}/${numbat_binary}" \
      ./cmd/numbat

  signed="false"
  if [[ -n "${codesign_identity}" ]]; then
    codesign \
      --force \
      --options runtime \
      --timestamp \
      --sign "${codesign_identity}" \
      "${preview_tmp}/${numbat_binary}"
    codesign --verify --strict --verbose=2 "${preview_tmp}/${numbat_binary}"
    signed="true"
  fi

  numbat_binary_sha256="$(
    shasum -a 256 "${preview_tmp}/${numbat_binary}" | awk '{print $1}'
  )"
  [[ "${numbat_binary_sha256}" =~ ^[0-9a-f]{64}$ ]] ||
    die "built Numbat checksum is not a lowercase SHA-256"

  env \
    CGO_ENABLED=0 \
    GOFLAGS= \
    GOOS="${target_os}" \
    GOARCH="${architecture}" \
    go -C "${repository_root}" build \
      -buildvcs=false \
      -trimpath \
      -ldflags="-s -w -X main.buildVersion=${preview_version} -X main.buildCommit=${belay_commit} -X main.bundledNumbatSHA256=${numbat_binary_sha256} -X main.bundledNumbatVersionMarker=${NUMBAT_VERSION_MARKER}" \
      -o "${preview_tmp}/${belay_binary}" \
      ./cmd/belay

  if [[ -n "${codesign_identity}" ]]; then
    codesign \
      --force \
      --options runtime \
      --timestamp \
      --sign "${codesign_identity}" \
      "${preview_tmp}/${belay_binary}"
    codesign --verify --strict --verbose=2 "${preview_tmp}/${belay_binary}"
  fi

  install -m 0644 "${repository_root}/LICENSE" "${package_root}/LICENSE"
  install -m 0644 "${repository_root}/licenses/numbat/LICENSE" "${package_root}/licenses/numbat/LICENSE"
  install -m 0644 \
    "${repository_root}/licenses/numbat/THIRD_PARTY_LICENSES.txt" \
    "${package_root}/licenses/numbat/THIRD_PARTY_LICENSES.txt"
  install -m 0644 "${repository_root}/README.md" "${package_root}/README.md"
  install -m 0644 "${repository_root}/llms.txt" "${package_root}/llms.txt"
  install -m 0644 \
    "${repository_root}/docs/launch/developer-preview.md" \
    "${package_root}/docs/developer-alpha.md"
  install -m 0644 \
    "${repository_root}/docs/launch/clean-machine-alpha-qa.md" \
    "${package_root}/docs/clean-machine-alpha-qa.md"
  if [[ "${target_os}" == "windows" ]]; then
    install -m 0644 \
      "${repository_root}/docs/launch/windows-port.md" \
      "${package_root}/docs/windows-port.md"
  fi

  cat > "${package_root}/BUILD-INFO.txt" <<EOF
Belay Local Developer Alpha
version=${preview_version}
target=${target_os}/${architecture}
belay_commit=${belay_commit}
belay_dirty=${belay_dirty}
numbat_commit=${NUMBAT_COMMIT}
numbat_binary_sha256=${numbat_binary_sha256}
numbat_version_marker=${NUMBAT_VERSION_MARKER}
source_date_epoch=${source_date_epoch}
signed=${signed}
notarized=false
EOF

  (
    cd -- "${package_root}"
    find . -type f ! -name SHA256SUMS -print |
      LC_ALL=C sort |
      while IFS= read -r path; do
        shasum -a 256 "${path#./}"
      done > SHA256SUMS
  )

  archive="${output_dir}/${package_name}.${archive_format}"
  go run "${repository_root}/scripts/package-preview.go" \
    -source "${package_root}" \
    -output "${archive}" \
    -epoch "${source_date_epoch}" \
    -format "${archive_format}"
  (
    cd -- "${output_dir}"
    shasum -a 256 "$(basename -- "${archive}")" > "$(basename -- "${archive}").sha256"
  )
  printf 'built %s\n' "${archive}"
  printf 'checksum %s.sha256\n' "${archive}"
done
