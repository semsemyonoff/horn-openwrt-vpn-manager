DOCKER_IMAGE ?= horn-vpn-manager-builder
OUTPUT_DIR   ?= bin

# OpenWrt release for ipk builds (23.05.5, 24.10.0, etc.)
OPENWRT_RELEASE ?= 23.05.5

SDK_URL_APK = https://downloads.openwrt.org/snapshots/targets/x86/64
SDK_URL_IPK = https://downloads.openwrt.org/releases/$(OPENWRT_RELEASE)/targets/x86/64

SHELL_SCRIPTS = \
	horn-vpn-manager/files/vpn-manager.sh \
	horn-vpn-manager/files/subs.sh \
	horn-vpn-manager/files/getdomains.sh \
	horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager

GO_PKG_DIR = horn-vpn-manager
GO_BIN     = vpn-manager

VOLUMES = \
	-v $(CURDIR)/horn-vpn-manager:/src/horn-vpn-manager:ro \
	-v $(CURDIR)/horn-vpn-manager-luci:/src/horn-vpn-manager-luci:ro

.PHONY: help docker-apk docker-ipk build build-ipk shell shell-ipk lint clean \
	go-build go-test go-lint go-fmt

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ── Docker images ─────────────────────────────────────────────

docker-apk: ## Build Docker image with OpenWrt SNAPSHOT SDK (apk)
	docker build --build-arg SDK_BASE_URL=$(SDK_URL_APK) \
		-t $(DOCKER_IMAGE):apk .

docker-ipk: ## Build Docker image with OpenWrt release SDK (ipk)
	docker build --build-arg SDK_BASE_URL=$(SDK_URL_IPK) \
		-t $(DOCKER_IMAGE):ipk .

# ── Build packages ────────────────────────────────────────────

build: docker-apk ## Build .apk packages (OpenWrt 25 / SNAPSHOT)
	@mkdir -p $(OUTPUT_DIR)
	docker run --rm $(VOLUMES) -v $(CURDIR)/$(OUTPUT_DIR):/out \
		$(DOCKER_IMAGE):apk all

build-ipk: docker-ipk ## Build .ipk packages (OpenWrt release, OPENWRT_RELEASE=23.05.5)
	@mkdir -p $(OUTPUT_DIR)
	docker run --rm $(VOLUMES) -v $(CURDIR)/$(OUTPUT_DIR):/out \
		$(DOCKER_IMAGE):ipk all

# ── Interactive shell ─────────────────────────────────────────

shell: docker-apk ## Shell inside SNAPSHOT SDK
	docker run --rm -it $(VOLUMES) $(DOCKER_IMAGE):apk shell

shell-ipk: docker-ipk ## Shell inside release SDK
	docker run --rm -it $(VOLUMES) $(DOCKER_IMAGE):ipk shell

# ── Lint ──────────────────────────────────────────────────────

lint: go-fmt go-lint ## Run all checks (Go + shell + JSON)
	@echo ">> Syntax check (sh -n)..."
	@for f in $(SHELL_SCRIPTS); do sh -n "$$f" && echo "   $$f: ok"; done
	@echo ">> Shellcheck..."
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck -s sh -S warning $(SHELL_SCRIPTS); \
		echo "   shellcheck: ok"; \
	else \
		echo "   shellcheck not found, skipping (install: brew install shellcheck)"; \
	fi
	@echo ">> JSON validation..."
	@for f in \
		horn-vpn-manager/files/config.template.json \
		horn-vpn-manager/files/subs.example.json \
		horn-vpn-manager/files/domains.example.json \
		horn-vpn-manager-luci/root/usr/share/rpcd/acl.d/horn-vpn-manager.json \
		horn-vpn-manager-luci/root/usr/share/luci/menu.d/horn-vpn-manager.json; \
	do jq . "$$f" > /dev/null && echo "   $$f: ok"; done
	@echo ">> All checks passed"

# ── Go development ───────────────────────────────────────────

go-build: ## Build vpn-manager binary to bin/
	@mkdir -p $(OUTPUT_DIR)
	cd $(GO_PKG_DIR) && go build -trimpath -ldflags='-s -w' -o ../$(OUTPUT_DIR)/$(GO_BIN) ./cmd/vpn-manager

go-test: ## Run Go tests
	cd $(GO_PKG_DIR) && go test ./... -count=1

go-lint: ## Run golangci-lint on Go code
	@if command -v golangci-lint >/dev/null 2>&1; then \
		cd $(GO_PKG_DIR) && golangci-lint run; \
	else \
		echo "golangci-lint not found (install: brew install golangci-lint)"; \
		exit 1; \
	fi

go-fmt: ## Check Go formatting
	@cd $(GO_PKG_DIR) && test -z "$$(gofmt -l .)" || { gofmt -d .; exit 1; }

# ── Cleanup ───────────────────────────────────────────────────

clean: ## Remove build output
	rm -rf $(OUTPUT_DIR)
