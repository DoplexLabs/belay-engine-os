#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly ALPHA_VERSION="0.0.1-alpha.11"
readonly NUMBAT_COMMIT="b5172bb8bb8f1d68edc4f3b9462de7e248dc5243"

die() {
  printf 'validate-alpha-surface: %s\n' "$*" >&2
  exit 1
}

require_file() {
  [[ -f "$1" ]] || die "required release-surface file is missing: $1"
}

require_text() {
  local path="$1"
  local text="$2"
  grep -Fq -- "${text}" "${path}" ||
    die "${path} does not contain required text: ${text}"
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repository_root="$(cd -- "${script_dir}/.." && pwd -P)"

command -v grep >/dev/null 2>&1 || die "required command not found: grep"

for path in \
  README.md \
  SECURITY.md \
  CONTRIBUTING.md \
  SUPPORT.md \
  llms.txt \
  docs/contracts/mcp-v1.md \
  docs/design/p0-09-one-command-onboarding.md \
  docs/implementation/briefs/14-p0-one-command-onboarding.md \
  docs/launch/developer-preview.md \
  docs/launch/clean-machine-alpha-qa.md \
  docs/launch/local-v0-requirements.md \
  docs/launch/windows-port.md \
  docs/launch/windows-clean-machine-alpha-qa.md \
  scripts/install.sh \
  scripts/install.ps1 \
  scripts/notarize-release.sh \
  scripts/render-homebrew-formula.sh \
  scripts/smoke-windows-preview.ps1 \
  .github/workflows/signed-alpha-release.yml; do
  require_file "${repository_root}/${path}"
done

for path in \
  README.md \
  docs/launch/developer-preview.md \
  Makefile \
  scripts/build-developer-preview.sh \
  .github/workflows/developer-preview.yml; do
  require_text "${repository_root}/${path}" "${ALPHA_VERSION}"
done

for path in \
  README.md \
  docs/launch/developer-preview.md \
  llms.txt \
  scripts/build-developer-preview.sh; do
  require_text "${repository_root}/${path}" "${NUMBAT_COMMIT}"
done

require_text "${repository_root}/README.md" "Apple Silicon"
require_text "${repository_root}/README.md" \
  "curl -fsSL https://getbelay.vercel.app/install | bash"
require_text "${repository_root}/docs/launch/developer-preview.md" "Apple Silicon"
require_text "${repository_root}/docs/launch/developer-preview.md" "unsigned"
require_text "${repository_root}/docs/launch/developer-preview.md" "Codex"
require_text "${repository_root}/docs/launch/developer-preview.md" "Claude Code"
require_text "${repository_root}/docs/launch/developer-preview.md" "Belay Teams is not included"
require_text "${repository_root}/docs/launch/local-v0-requirements.md" "licensed under MIT"
for path in \
  README.md \
  docs/launch/developer-preview.md; do
  require_text "${repository_root}/${path}" "Windows"
  require_text "${repository_root}/${path}" "SmartScreen"
done
for path in \
  README.md \
  llms.txt \
  .github/workflows/developer-preview.yml; do
  require_text "${repository_root}/${path}" "install.ps1"
done
require_text "${repository_root}/.github/workflows/developer-preview.yml" \
  "windows-amd64.zip"
require_text "${repository_root}/Makefile" "--os windows"
require_text "${repository_root}/scripts/build-developer-preview.sh" "windows"
require_text "${repository_root}/SECURITY.md" "DPAPI"
require_text "${repository_root}/docs/storage/local-storage-lifecycle.md" "DPAPI"
require_text "${repository_root}/README.md" "stable ingestion snapshot"
require_text "${repository_root}/docs/launch/developer-preview.md" "stable ingestion snapshot"
require_text "${repository_root}/docs/launch/local-v0-requirements.md" "next_cursor"
require_text "${repository_root}/README.md" "./bin/belay quickstart"
require_text "${repository_root}/docs/launch/developer-preview.md" "./bin/belay quickstart"
require_text "${repository_root}/docs/launch/clean-machine-alpha-qa.md" "./bin/belay quickstart"
require_text "${repository_root}/README.md" "./bin/belay quickstart --no-mcp"
require_text "${repository_root}/README.md" \
  "./bin/belay quickstart --allow-codex-mcp-add"
require_text "${repository_root}/docs/launch/developer-preview.md" \
  "./bin/belay quickstart --no-mcp"
require_text "${repository_root}/docs/launch/developer-preview.md" \
  "./bin/belay quickstart --allow-codex-mcp-add"
require_text "${repository_root}/docs/launch/clean-machine-alpha-qa.md" \
  "./bin/belay quickstart --no-mcp"
require_text "${repository_root}/docs/launch/clean-machine-alpha-qa.md" \
  "./bin/belay quickstart --allow-codex-mcp-add"
require_text "${repository_root}/README.md" "./bin/belay local"
require_text "${repository_root}/docs/launch/developer-preview.md" "./bin/belay local"
require_text "${repository_root}/docs/launch/local-v0-requirements.md" "belay quickstart"
for path in \
  README.md \
  llms.txt \
  docs/launch/developer-preview.md \
  docs/launch/clean-machine-alpha-qa.md \
  docs/design/p0-09-one-command-onboarding.md \
  docs/implementation/briefs/14-p0-one-command-onboarding.md \
  docs/contracts/mcp-v1.md; do
  require_text "${repository_root}/${path}" "mcp-config install"
  require_text "${repository_root}/${path}" "mcp-config status"
  require_text "${repository_root}/${path}" "mcp-config uninstall"
  require_text "${repository_root}/${path}" "allow-codex-mcp-add"
done
require_text "${repository_root}/docs/launch/local-v0-requirements.md" \
  "allow-codex-mcp-add"
for path in \
  README.md \
  llms.txt \
  docs/launch/developer-preview.md \
  docs/launch/clean-machine-alpha-qa.md \
  docs/design/p0-09-one-command-onboarding.md \
  docs/implementation/briefs/14-p0-one-command-onboarding.md \
  docs/contracts/mcp-v1.md; do
  require_text "${repository_root}/${path}" "non-atomic"
  require_text "${repository_root}/${path}" "duplicate-name behavior"
done
for path in \
  README.md \
  docs/launch/developer-preview.md \
  docs/contracts/mcp-v1.md; do
  for tool in \
    list_sessions \
    get_session \
    get_session_timeline \
    query_activity \
    list_findings \
    get_stats \
    list_issues \
    get_issue \
    lookup_session_events \
    get_top_issues \
    get_issue_excerpts \
    get_fix_status \
    get_mission_pack \
    record_mission_pack_accepted \
    get_mission_pack_status \
    list_experience_proposals \
    list_active_experiences \
    approve_experience \
    resolve_experience_proposal \
    prepare_experience_lifecycle \
    apply_experience_lifecycle \
    propose_fix \
    record_fix_applied; do
    require_text "${repository_root}/${path}" "${tool}"
  done
done
for path in \
  README.md \
  llms.txt \
  docs/launch/developer-preview.md \
  docs/contracts/mcp-v1.md; do
  require_text "${repository_root}/${path}" "Cursor"
done
for path in \
  README.md \
  docs/contracts/mcp-v1.md; do
  require_text "${repository_root}/${path}" "~/.cursor/mcp.json"
done
for path in \
  README.md \
  llms.txt \
  docs/launch/developer-preview.md \
  docs/contracts/mcp-v1.md; do
  require_text "${repository_root}/${path}" "Antigravity"
done
for path in \
  README.md \
  docs/contracts/mcp-v1.md; do
  require_text "${repository_root}/${path}" "~/.gemini/config/mcp_config.json"
done
require_text "${repository_root}/README.md" "foreign or unverifiable"
require_text "${repository_root}/docs/launch/developer-preview.md" \
  "foreign, scope-ambiguous"
require_text "${repository_root}/docs/launch/developer-preview.md" \
  "unverifiable entry"
require_text "${repository_root}/docs/contracts/mcp-v1.md" \
  "MCP uninstall does not remove monitor hooks"
require_text "${repository_root}/scripts/build-developer-preview.sh" \
  "main.bundledNumbatSHA256"
require_text "${repository_root}/scripts/build-developer-preview.sh" \
  "main.bundledNumbatVersionMarker"
require_text "${repository_root}/scripts/build-developer-preview.sh" \
  "main.buildVersion"
require_text "${repository_root}/scripts/install.sh" \
  'metadata_value "${build_info}" signed'
require_text "${repository_root}/scripts/install.sh" \
  'metadata_value "${build_info}" notarized'
require_text "${repository_root}/scripts/notarize-release.sh" \
  'xcrun notarytool submit'
require_text "${repository_root}/scripts/install.sh" \
  'readonly REPOSITORY="DoplexLabs/belay"'
require_text "${repository_root}/scripts/render-homebrew-formula.sh" \
  'DoplexLabs/belay/releases/download'
require_text "${repository_root}/.github/workflows/signed-alpha-release.yml" \
  'scripts/notarize-release.sh'
require_text "${repository_root}/.github/workflows/signed-alpha-release.yml" \
  'gh release create'
require_text "${repository_root}/.github/workflows/signed-alpha-release.yml" \
  'DoplexLabs/belay'
require_text "${repository_root}/.github/workflows/developer-preview.yml" \
  'if: ${{ inputs.publish }}'
require_text "${repository_root}/.github/workflows/developer-preview.yml" \
  'gh release create'
require_text "${repository_root}/.github/workflows/developer-preview.yml" \
  'DoplexLabs/belay'
require_text "${repository_root}/scripts/smoke-developer-preview.sh" \
  '"${belay}" agents'
require_text "${repository_root}/.github/workflows/developer-preview.yml" \
  'test "$(uname -m)" = "arm64"'

if grep -Eq \
  'NUMBAT_SHA256=|--numbat-sha256|--numbat-version-marker' \
  "${repository_root}/README.md" \
  "${repository_root}/docs/launch/developer-preview.md" \
  "${repository_root}/docs/launch/clean-machine-alpha-qa.md"; then
  die "release onboarding still requires a manual Numbat hash or version marker"
fi

if grep -Eq \
  -- '--numbat([[:space:]]|=)' \
  "${repository_root}/scripts/smoke-developer-preview.sh"; then
  die "packaged smoke must resolve sibling Numbat without a manual path"
fi

if grep -Eiq \
  'cursor pagination is (not implemented|unavailable)|cursor paging remains unavailable|resource-filtered activity may be a bounded subset|sparse resource filters can return a bounded subset' \
  "${repository_root}/README.md" \
  "${repository_root}/SUPPORT.md" \
  "${repository_root}/llms.txt" \
  "${repository_root}/docs/launch/developer-preview.md" \
  "${repository_root}/docs/launch/local-v0-requirements.md"; then
  die "release documentation contains a stale pagination or resource-filter limitation"
fi

if grep -Eiq \
  '(six|nine|thirteen|fifteen|exactly [0-9]+)[- ]tool MCP|MCP (still )?(exposes|remains|advertises)( exactly| the expected)? (six|nine|thirteen|fifteen)|MCP (server )?(is|remains) read-only|read-only (stdio )?MCP server|two additive tools' \
  "${repository_root}/README.md" \
  "${repository_root}/SECURITY.md" \
  "${repository_root}/CONTRIBUTING.md" \
  "${repository_root}/SUPPORT.md" \
  "${repository_root}/llms.txt" \
  "${repository_root}/docs/contracts/mcp-v1.md" \
  "${repository_root}/docs/launch/developer-preview.md" \
  "${repository_root}/docs/launch/clean-machine-alpha-qa.md"; then
  die "current release documentation contains a stale numeric or read-only MCP claim"
fi

if grep -Eiq \
  'quickstart registers (the )?(stdio server|Belay MCP) automatically for (each )?detected supported agent|registers the existing read-only MCP server in detected Codex and Claude Code user configuration|quickstart success registers detected agents|attempts ownership-safe user-scope registration for detected Codex and Claude Code' \
  "${repository_root}/README.md" \
  "${repository_root}/llms.txt" \
  "${repository_root}/docs/contracts/mcp-v1.md" \
  "${repository_root}/docs/design/p0-09-one-command-onboarding.md" \
  "${repository_root}/docs/implementation/briefs/14-p0-one-command-onboarding.md" \
  "${repository_root}/docs/launch/developer-preview.md" \
  "${repository_root}/docs/launch/clean-machine-alpha-qa.md" \
  "${repository_root}/docs/launch/local-v0-requirements.md"; then
  die "release documentation overstates automatic Codex MCP registration"
fi

for path in \
  README.md \
  llms.txt \
  docs/contracts/mcp-v1.md \
  docs/design/p0-09-one-command-onboarding.md \
  docs/implementation/briefs/14-p0-one-command-onboarding.md \
  docs/launch/developer-preview.md \
  docs/launch/clean-machine-alpha-qa.md \
  docs/launch/local-v0-requirements.md; do
  require_text "${repository_root}/${path}" "mcp-config uninstall"
done

if grep -Eiq \
  'when updating a prior Codex registration|Updating Codex requires|Codex update additionally requires|then use the same update/rollback flow|archive move/recognized update with the Codex opt-in|verified identity is updated safely|Codex update requires.*allow-codex-mcp-add' \
  "${repository_root}/README.md" \
  "${repository_root}/llms.txt" \
  "${repository_root}/docs/contracts/mcp-v1.md" \
  "${repository_root}/docs/design/p0-09-one-command-onboarding.md" \
  "${repository_root}/docs/implementation/briefs/14-p0-one-command-onboarding.md" \
  "${repository_root}/docs/launch/developer-preview.md" \
  "${repository_root}/docs/launch/clean-machine-alpha-qa.md" \
  "${repository_root}/docs/launch/local-v0-requirements.md"; then
  die "release documentation incorrectly allows Codex registration migration"
fi

if grep -Eiq \
  'windows[^.]*(out of scope|outside the alpha)' \
  "${repository_root}/README.md" \
  "${repository_root}/SUPPORT.md" \
  "${repository_root}/SECURITY.md" \
  "${repository_root}/llms.txt" \
  "${repository_root}/docs/launch/developer-preview.md"; then
  die "release documentation still excludes Windows from the supported alpha"
fi

# Semantic analysis may now run through the Cursor CLI and the Antigravity CLI,
# but neither CLI was installed on the development machine, so the release
# surface must keep saying that the path was not validated against a real run.
for path in \
  README.md \
  docs/launch/developer-preview.md; do
  require_text "${repository_root}/${path}" "not validated against a real"
done

if grep -Eiq \
  'antigravity transcript reader (is|was|has been|remains) (built|validated|implemented|available|shipped|included)|(built|validated|implemented|ships|shipped|includes|added) (an|the|its) antigravity transcript reader|antigravity (history|historical) scan (is|was|has been) (validated|implemented|run)' \
  "${repository_root}/README.md" \
  "${repository_root}/SUPPORT.md" \
  "${repository_root}/SECURITY.md" \
  "${repository_root}/llms.txt" \
  "${repository_root}/docs/contracts/mcp-v1.md" \
  "${repository_root}/docs/contracts/telemetry-v1.md" \
  "${repository_root}/docs/launch/developer-preview.md" \
  "${repository_root}/docs/launch/windows-port.md" \
  "${repository_root}/docs/launch/clean-machine-alpha-qa.md" \
  "${repository_root}/docs/launch/windows-clean-machine-alpha-qa.md"; then
  die "release documentation claims an Antigravity transcript reader or historical scan exists"
fi

if grep -Fq 'if: runner.arch' \
  "${repository_root}/.github/workflows/developer-preview.yml"; then
  die "developer-alpha workflow may not conditionally skip native smoke"
fi

printf 'alpha release-surface validation passed\n'
