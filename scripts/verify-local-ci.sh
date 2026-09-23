#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

go_cache="${GOCACHE:-/tmp/belay-engine-go-cache}"
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/belay-local-ci.XXXXXX")"
trap 'rm -rf "${build_dir}"' EXIT

echo "local-ci: privacy guard"
scripts/verify-privacy.sh

echo "local-ci: tests"
CGO_ENABLED=0 GOCACHE="${go_cache}" go test -count=1 ./...

echo "local-ci: race-sensitive tests"
GOCACHE="${go_cache}" go test -race \
  ./internal/storage/local \
  ./internal/pipeline \
  ./internal/localapp

echo "local-ci: vet and static build"
GOCACHE="${go_cache}" go vet ./...
CGO_ENABLED=0 GOCACHE="${go_cache}" go build \
  -trimpath \
  -o "${build_dir}/belay" \
  ./cmd/belay

echo "local-ci: browser and diff hygiene"
node --check internal/presentation/localhttp/assets/app.js
git diff --check

echo "local-ci: release surface"
make verify-release-surface

echo "local-ci: passed"
