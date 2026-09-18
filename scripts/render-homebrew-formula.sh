#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

die() {
  printf 'render-homebrew-formula: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
usage: render-homebrew-formula.sh --version VERSION --sha256 SHA256 --output PATH

Render the public DoplexLabs Homebrew formula for a verified Belay release.
EOF
}

version=""
sha256=""
output=""
while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || die "--version requires a value"
      version="$2"
      shift 2
      ;;
    --sha256)
      (($# >= 2)) || die "--sha256 requires a value"
      sha256="$2"
      shift 2
      ;;
    --output)
      (($# >= 2)) || die "--output requires a value"
      output="$2"
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

[[ "${version}" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]] ||
  die "invalid version"
[[ "${sha256}" =~ ^[0-9a-f]{64}$ ]] ||
  die "sha256 must be a lowercase SHA-256"
[[ -n "${output}" ]] || die "--output is required"

mkdir -p -- "$(dirname -- "${output}")"
cat > "${output}" <<EOF
class Belay < Formula
  desc "Private local intelligence for Claude Code and Codex sessions"
  homepage "https://getbelay.vercel.app"
  url "https://github.com/DoplexLabs/belay/releases/download/v${version}/belay-local-developer-alpha-v${version}-darwin-arm64.tar.gz"
  version "${version}"
  sha256 "${sha256}"
  license "MIT"

  depends_on arch: :arm64
  depends_on :macos

  def install
    libexec.install Dir["*"]
    (bin/"belay").write <<~SH
      #!/bin/sh
      export BELAY_EXECUTABLE_PATH="#{opt_libexec}/bin/belay"
      exec "#{opt_libexec}/bin/belay" "\$@"
    SH
  end

  test do
    assert_match "belay #{version}", shell_output("#{bin}/belay version")
  end
end
EOF

printf 'rendered %s\n' "${output}"
