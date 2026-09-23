ALPHA_VERSION ?= 0.0.1-alpha.11
ALPHA_ARCH ?= arm64
ALPHA_OUTPUT ?= ./dist
WINDOWS_ARCH ?= amd64

.PHONY: test verify verify-local-ci verify-release-surface verify-windows verify-windows-scripts test-install-windows preview preview-all preview-windows smoke-preview alpha-readiness

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

verify-local-ci:
	scripts/verify-local-ci.sh

# Cross-compile and vet every package for the Windows engineering target.
# Native Windows tests run in the ci.yml windows job.
verify-windows: verify-windows-scripts
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./...

# PowerShell has no `bash -n`; parse each script with the PowerShell parser
# instead. A developer machine without pwsh skips the check with a notice.
verify-windows-scripts:
	@if command -v pwsh >/dev/null 2>&1; then \
		pwsh -NoProfile -Command '$$bad = $$false; foreach ($$file in @("scripts/install.ps1", "scripts/install_windows_test.ps1", "scripts/smoke-windows-preview.ps1")) { $$errs = $$null; [System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path $$file).Path, [ref]$$null, [ref]$$errs) | Out-Null; if ($$errs) { Write-Output $$file; $$errs | ForEach-Object { Write-Output $$_.ToString() }; $$bad = $$true } }; if ($$bad) { exit 1 }; Write-Output "powershell scripts parse cleanly"'; \
	else \
		echo "verify-windows-scripts: pwsh not found; skipping PowerShell parse checks"; \
	fi

# Contract test for scripts/install.ps1. Runs anywhere pwsh is installed.
test-install-windows:
	@if command -v pwsh >/dev/null 2>&1; then \
		pwsh -NoProfile -File scripts/install_windows_test.ps1; \
	else \
		echo "test-install-windows: pwsh not found; skipping installer contract test"; \
	fi

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

preview-windows:
	scripts/build-developer-preview.sh \
		--os windows \
		--version "$(ALPHA_VERSION)" \
		--arch "$(WINDOWS_ARCH)" \
		--output-dir "$(ALPHA_OUTPUT)"

smoke-preview:
	@test -n "$(ARCHIVE)" || (echo "usage: make smoke-preview ARCHIVE=path/to/archive.tar.gz" >&2; exit 1)
	scripts/smoke-developer-preview.sh "$(ARCHIVE)"

alpha-readiness:
	scripts/alpha-readiness.sh \
		--version "$(ALPHA_VERSION)" \
		--output-dir "$(ALPHA_OUTPUT)"
