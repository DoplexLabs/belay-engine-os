ALPHA_VERSION ?= 0.0.1-alpha.8
ALPHA_ARCH ?= arm64
ALPHA_OUTPUT ?= ./dist

.PHONY: test verify verify-release-surface preview preview-all smoke-preview alpha-readiness

test:
	CGO_ENABLED=0 go test -count=1 ./...

verify: test
	go vet ./...
	bash -n scripts/build-developer-preview.sh
	bash -n scripts/smoke-developer-preview.sh
	bash -n scripts/alpha-readiness.sh
	bash -n scripts/alpha-readiness_test.sh
	bash -n scripts/install.sh
	bash -n scripts/notarize-release.sh
	bash -n scripts/render-homebrew-formula.sh
	bash -n scripts/validate-alpha-surface.sh
	scripts/alpha-readiness_test.sh
	scripts/validate-alpha-surface.sh

verify-release-surface:
	bash -n scripts/build-developer-preview.sh
	bash -n scripts/smoke-developer-preview.sh
	bash -n scripts/alpha-readiness.sh
	bash -n scripts/alpha-readiness_test.sh
	bash -n scripts/install.sh
	bash -n scripts/notarize-release.sh
	bash -n scripts/render-homebrew-formula.sh
	bash -n scripts/validate-alpha-surface.sh
	scripts/alpha-readiness_test.sh
	scripts/validate-alpha-surface.sh

preview:
	scripts/build-developer-preview.sh \
		--version "$(ALPHA_VERSION)" \
		--arch "$(ALPHA_ARCH)" \
		--output-dir "$(ALPHA_OUTPUT)"

preview-all:
	scripts/build-developer-preview.sh \
		--version "$(ALPHA_VERSION)" \
		--arch all \
		--output-dir "$(ALPHA_OUTPUT)"

smoke-preview:
	@test -n "$(ARCHIVE)" || (echo "usage: make smoke-preview ARCHIVE=path/to/archive.tar.gz" >&2; exit 1)
	scripts/smoke-developer-preview.sh "$(ARCHIVE)"

alpha-readiness:
	scripts/alpha-readiness.sh \
		--version "$(ALPHA_VERSION)" \
		--output-dir "$(ALPHA_OUTPUT)"
