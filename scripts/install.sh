#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly DEFAULT_VERSION="0.0.1-alpha.11"
readonly REPOSITORY="DoplexLabs/belay"

die() {
  printf 'belay-install: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
usage: install.sh [options]

Install or update Belay for the current macOS user.

Options:
  --version VERSION             Release version (default: 0.0.1-alpha.11)
  --quickstart                  Start private Belay onboarding after install
  --allow-codex-mcp-add         Pass the explicit Codex MCP opt-in to quickstart
  --uninstall                   Remove the program, monitor hooks, and Belay MCP
  --archive PATH                Install a local archive instead of downloading
  --checksum PATH               Checksum file for --archive
  --install-root PATH           Program root (default: ~/.local/share/belay)
  --bin-dir PATH                Command directory (default: ~/.local/bin)
  --allow-untrusted             DEVELOPMENT ONLY: accept unsigned test archives
  -h, --help                    Show this help

Uninstall preserves encrypted Belay history and its Keychain data.
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

version="${BELAY_VERSION:-${DEFAULT_VERSION}}"
install_root="${BELAY_INSTALL_ROOT:-${HOME:-}/.local/share/belay}"
bin_dir="${BELAY_BIN_DIR:-${HOME:-}/.local/bin}"
archive_source=""
checksum_source=""
quickstart="false"
allow_codex_mcp_add="false"
uninstall="false"
allow_untrusted="${BELAY_INSTALL_ALLOW_UNTRUSTED:-0}"

while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || die "--version requires a value"
      version="$2"
      shift 2
      ;;
    --quickstart)
      quickstart="true"
      shift
      ;;
    --allow-codex-mcp-add)
      allow_codex_mcp_add="true"
      shift
      ;;
    --uninstall)
      uninstall="true"
      shift
      ;;
    --archive)
      (($# >= 2)) || die "--archive requires a value"
      archive_source="$2"
      shift 2
      ;;
    --checksum)
      (($# >= 2)) || die "--checksum requires a value"
      checksum_source="$2"
      shift 2
      ;;
    --install-root)
      (($# >= 2)) || die "--install-root requires a value"
      install_root="$2"
      shift 2
      ;;
    --bin-dir)
      (($# >= 2)) || die "--bin-dir requires a value"
      bin_dir="$2"
      shift 2
      ;;
    --allow-untrusted)
      allow_untrusted="1"
      shift
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

[[ -n "${HOME:-}" ]] || die "HOME is not set"
[[ "${version}" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]] ||
  die "version may contain only letters, numbers, dots, underscores, and hyphens"
[[ "${install_root}" == /* && "${bin_dir}" == /* ]] ||
  die "install paths must be absolute"
case "${install_root}" in
  /|/Applications|/Library|/System|/Users|/usr|/usr/local)
    die "refusing broad install root: ${install_root}"
    ;;
esac
case "${bin_dir}" in
  /|/Applications|/Library|/System|/Users|/usr)
    die "refusing broad command directory: ${bin_dir}"
    ;;
esac

runtime_root="${install_root}/runtime"
installed_belay="${runtime_root}/bin/belay"
command_path="${bin_dir}/belay"

if [[ "${uninstall}" == "true" ]]; then
  if [[ -x "${installed_belay}" ]]; then
    "${installed_belay}" mcp-config uninstall >/dev/null 2>&1 || true
    "${installed_belay}" hooks uninstall >/dev/null 2>&1 || true
  fi
  if [[ -L "${command_path}" ]]; then
    command_target="$(readlink "${command_path}")"
    if [[ "${command_target}" == "${installed_belay}" ]]; then
      rm -f -- "${command_path}"
    else
      die "refusing to remove foreign command link: ${command_path}"
    fi
  elif [[ -e "${command_path}" ]]; then
    die "refusing to remove foreign command: ${command_path}"
  fi
  if [[ -e "${runtime_root}" ]]; then
    rm -rf -- "${runtime_root}"
  fi
  rmdir "${install_root}" >/dev/null 2>&1 || true
  printf 'Belay was uninstalled. Encrypted history under ~/.belay was preserved.\n'
  exit 0
fi

[[ "$(uname -s)" == "Darwin" ]] || die "Belay currently supports macOS only"
[[ "$(uname -m)" == "arm64" ]] || die "this alpha currently supports Apple Silicon only"

for required in awk cp curl cut grep ln mkdir mktemp mv readlink rm rmdir shasum sort tar uname; do
  require_command "${required}"
done

if [[ -e "${command_path}" ]]; then
  if [[ ! -L "${command_path}" ]] ||
    [[ "$(readlink "${command_path}")" != "${installed_belay}" ]]; then
    die "refusing to replace existing command: ${command_path}"
  fi
fi

install_tmp="$(mktemp -d "${TMPDIR:-/tmp}/belay-install.XXXXXX")"
stage_root=""
backup_root=""
runtime_swapped="false"
install_complete="false"
cleanup() {
  rm -rf -- "${install_tmp}"
  if [[ -n "${stage_root}" && -e "${stage_root}" ]]; then
    rm -rf -- "${stage_root}"
  fi
  if [[ "${install_complete}" != "true" && "${runtime_swapped}" == "true" ]]; then
    rm -rf -- "${runtime_root}"
    if [[ -n "${backup_root}" && -e "${backup_root}" ]]; then
      mv -- "${backup_root}" "${runtime_root}"
    fi
  fi
}
trap cleanup EXIT

archive_name="belay-local-developer-alpha-v${version}-darwin-arm64.tar.gz"
archive="${install_tmp}/${archive_name}"
checksum="${archive}.sha256"

if [[ -n "${archive_source}" ]]; then
  [[ -f "${archive_source}" ]] || die "archive not found: ${archive_source}"
  if [[ -z "${checksum_source}" ]]; then
    checksum_source="${archive_source}.sha256"
  fi
  [[ -f "${checksum_source}" ]] || die "checksum not found: ${checksum_source}"
  cp -- "${archive_source}" "${archive}"
  cp -- "${checksum_source}" "${checksum}"
else
  release_base="${BELAY_DOWNLOAD_BASE_URL:-https://github.com/${REPOSITORY}/releases/download/v${version}}"
  printf 'Downloading Belay %s…\n' "${version}"
  curl --fail --location --retry 3 --silent --show-error \
    --output "${archive}" \
    "${release_base}/${archive_name}"
  curl --fail --location --retry 3 --silent --show-error \
    --output "${checksum}" \
    "${release_base}/${archive_name}.sha256"
fi

(
  cd -- "${install_tmp}"
  shasum -a 256 -c "${archive_name}.sha256"
)

listing="${install_tmp}/archive-contents.txt"
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

tar -xzf "${archive}" -C "${install_tmp}"
package_root="${install_tmp}/${top_levels}"
build_info="${package_root}/BUILD-INFO.txt"
[[ -x "${package_root}/bin/belay" && -x "${package_root}/bin/numbat" ]] ||
  die "archive does not contain executable Belay and Numbat binaries"
[[ -f "${build_info}" && -f "${package_root}/SHA256SUMS" ]] ||
  die "archive is missing release metadata"
(
  cd -- "${package_root}"
  shasum -a 256 -c SHA256SUMS
)

[[ "$(metadata_value "${build_info}" version)" == "${version}" ]] ||
  die "archive version does not match requested version"
[[ "$(metadata_value "${build_info}" target)" == "darwin/arm64" ]] ||
  die "archive target is not darwin/arm64"
[[ "$(metadata_value "${build_info}" belay_dirty)" == "false" ]] ||
  die "refusing a dirty build"
if [[ "${allow_untrusted}" != "1" ]]; then
  [[ "$(metadata_value "${build_info}" signed)" == "true" ]] ||
    die "refusing an unsigned build"
  [[ "$(metadata_value "${build_info}" notarized)" == "true" ]] ||
    die "refusing a build that has not passed Apple notarization"
fi

mkdir -p -- "${install_root}" "${bin_dir}"
stage_root="$(mktemp -d "${install_root}/.runtime-stage.XXXXXX")"
cp -R "${package_root}/." "${stage_root}/"

if [[ -e "${runtime_root}" ]]; then
  backup_root="${install_root}/.runtime-backup.$$"
  [[ ! -e "${backup_root}" ]] || die "stale install backup exists: ${backup_root}"
  mv -- "${runtime_root}" "${backup_root}"
fi
mv -- "${stage_root}" "${runtime_root}"
stage_root=""
runtime_swapped="true"

link_tmp="${bin_dir}/.belay-link.$$"
rm -f -- "${link_tmp}"
ln -s "${installed_belay}" "${link_tmp}"
mv -f -- "${link_tmp}" "${command_path}"

if [[ -n "${backup_root}" && -e "${backup_root}" ]]; then
  rm -rf -- "${backup_root}"
fi
backup_root=""
install_complete="true"

installed_version="$("${command_path}" version)"
printf 'Installed %s at %s\n' "${installed_version}" "${command_path}"

if [[ "${quickstart}" == "true" ]]; then
  printf '%s\n' \
    "Starting private onboarding: Belay will install reversible monitor hooks," \
    "supported agent skills, and ownership-safe local MCP configuration."
  quickstart_args=("quickstart")
  if [[ "${allow_codex_mcp_add}" == "true" ]]; then
    quickstart_args+=("--allow-codex-mcp-add")
  fi
  exec "${command_path}" "${quickstart_args[@]}"
fi

case ":${PATH}:" in
  *":${bin_dir}:"*)
    printf 'Next: belay quickstart\n'
    ;;
  *)
    printf 'Next: %s quickstart\n' "${command_path}"
    printf 'Add %s to PATH to run belay from any terminal.\n' "${bin_dir}"
    ;;
esac
