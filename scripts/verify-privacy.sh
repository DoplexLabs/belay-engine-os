#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

failed=0
identity_revision="HEAD"

if [[ -n "${BELAY_PRIVACY_BASE:-}" ]]; then
  identity_revision="${BELAY_PRIVACY_BASE}..HEAD"
elif [[ -n "${GITHUB_EVENT_BEFORE:-}" ]] &&
  [[ "${GITHUB_EVENT_BEFORE}" != "0000000000000000000000000000000000000000" ]] &&
  git rev-parse --verify --quiet "${GITHUB_EVENT_BEFORE}^{commit}" >/dev/null; then
  identity_revision="${GITHUB_EVENT_BEFORE}..HEAD"
elif git rev-parse --verify --quiet refs/remotes/origin/main >/dev/null; then
  privacy_base="$(git merge-base refs/remotes/origin/main HEAD || true)"
  if [[ -n "${privacy_base}" && "${privacy_base}" != "$(git rev-parse HEAD)" ]]; then
    identity_revision="${privacy_base}..HEAD"
  elif git rev-parse --verify --quiet HEAD^ >/dev/null; then
    identity_revision="HEAD^..HEAD"
  fi
fi

while IFS=$'\t' read -r commit author_name author_email committer_name committer_email; do
  for identity in \
    "${author_name}<${author_email}>" \
    "${committer_name}<${committer_email}>"; do
    case "${identity}" in
      "rkat7-v2<290234671+rkat7-v2@users.noreply.github.com>"|\
      "samsat701<145064993+samsat701@users.noreply.github.com>"|\
      "Samyak<145064993+samsat701@users.noreply.github.com>"|\
      "Claude<noreply@anthropic.com>"|\
      "GitHub<noreply@github.com>")
        ;;
      *)
        echo "unapproved commit identity in ${commit}: ${identity}" >&2
        failed=1
        ;;
    esac
  done
done < <(git log "${identity_revision}" --format='%H%x09%an%x09%ae%x09%cn%x09%ce')

if git grep -I -n -E '/(Users|home)/[A-Za-z0-9._-]+/' \
  HEAD -- . ':(exclude)testdata/**' ':(exclude)*_test.go'; then
  echo "personal home-directory path found in tracked content" >&2
  failed=1
fi
if git grep -I -n -E \
  '[A-Za-z0-9._%+-]+@(gmail|hotmail|outlook|yahoo)\.[A-Za-z]{2,}' \
  HEAD -- . ':(exclude)testdata/**' ':(exclude)*_test.go'; then
  echo "personal-email provider found in tracked content" >&2
  failed=1
fi

exit "${failed}"
