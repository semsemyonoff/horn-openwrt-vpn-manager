DOCKER_IMAGE    ?= horn-vpn-manager-builder
OUTPUT_DIR      ?= bin

# OpenWrt target in target/subtarget format
TARGET          ?= mediatek/filogic

# OpenWrt release for ipk builds (23.05.5, 24.10.0, etc.)
OPENWRT_RELEASE ?= 23.05.5

# Go cross-compilation settings (auto-detected from TARGET, can be overridden)
#   arm64 : mediatek/*, rockchip/*, sunxi/*, mvebu/*, ipq807x/*, bcm27xx/bcm2710
#   mipsle: ramips/*  (softfloat)
#   mips  : ath79/*   (softfloat)
#   arm   : ipq40xx/*, bcm27xx/bcm2709  (ARMv7)
#   amd64 : x86/64
#   386   : x86/generic
ifneq ($(filter mediatek/% rockchip/% sunxi/% mvebu/% ipq807x/% bcm27xx/bcm2710,$(TARGET)),)
  GOARCH ?= arm64
else ifneq ($(filter ramips/%,$(TARGET)),)
  GOARCH ?= mipsle
  GOMIPS ?= softfloat
else ifneq ($(filter ath79/%,$(TARGET)),)
  GOARCH ?= mips
  GOMIPS ?= softfloat
else ifneq ($(filter ipq40xx/% bcm27xx/bcm2709,$(TARGET)),)
  GOARCH ?= arm
  GOARM  ?= 7
else ifeq ($(TARGET),x86/64)
  GOARCH ?= amd64
else ifeq ($(TARGET),x86/generic)
  GOARCH ?= 386
else
  GOARCH ?= arm64
endif

GOARM  ?=
GOMIPS ?=

# OpenWrt package architecture (for .ipk/.apk metadata)
ifeq ($(GOARCH),arm64)
  PKG_ARCH ?= aarch64_cortex-a53
else ifeq ($(GOARCH),amd64)
  PKG_ARCH ?= x86_64
else ifeq ($(GOARCH),386)
  PKG_ARCH ?= i386_pentium4
else ifeq ($(GOARCH),mipsle)
  PKG_ARCH ?= mipsel_24kc
else ifeq ($(GOARCH),mips)
  PKG_ARCH ?= mips_24kc
else ifeq ($(GOARCH),arm)
  PKG_ARCH ?= arm_cortex-a7_neon-vfpv4
else
  PKG_ARCH ?= all
endif

# Platform label for filenames (e.g. linux-arm64, linux-mipsle-softfloat)
ifeq ($(GOARCH),arm)
  PKG_PLATFORM ?= linux-armv$(GOARM)
else ifneq ($(GOMIPS),)
  PKG_PLATFORM ?= linux-$(GOARCH)-$(GOMIPS)
else
  PKG_PLATFORM ?= linux-$(GOARCH)
endif

PKG_VERSION ?= $(shell grep '^PKG_VERSION' horn-vpn-manager/Makefile | sed 's/PKG_VERSION[ ]*:=[ ]*//')
PKG_RELEASE ?= 1

TARGET_TAG       = $(subst /,-,$(TARGET))

SDK_URL_APK = https://downloads.openwrt.org/snapshots/targets/$(TARGET)
SDK_URL_IPK = https://downloads.openwrt.org/releases/$(OPENWRT_RELEASE)/targets/$(TARGET)

IMAGE_APK = $(DOCKER_IMAGE)-$(TARGET_TAG):apk
IMAGE_IPK = $(DOCKER_IMAGE)-$(TARGET_TAG):ipk

GO_PKG_DIR = horn-vpn-manager
GO_BIN     = vpn-manager

VOLUMES = \
	-v $(CURDIR)/horn-vpn-manager:/src/horn-vpn-manager:ro \
	-v $(CURDIR)/horn-vpn-manager-luci:/src/horn-vpn-manager-luci:ro

DOCKER_BUILD = docker build --platform linux/amd64

DOCKER_RUN = docker run --rm --platform linux/amd64 \
	$(VOLUMES) -v $(CURDIR)/$(OUTPUT_DIR):/out

# All platforms to build: GOARCH,GOARM,GOMIPS,PKG_ARCH,PLATFORM_LABEL
ALL_PLATFORMS = \
	arm64,,,aarch64_cortex-a53,linux-arm64 \
	amd64,,,x86_64,linux-amd64 \
	mipsle,,softfloat,mipsel_24kc,linux-mipsle-softfloat \
	mips,,softfloat,mips_24kc,linux-mips-softfloat \
	arm,7,,arm_cortex-a7_neon-vfpv4,linux-armv7

.PHONY: help build build-core build-ipk-core build-all build-ipk-all \
	build-luci build-ipk-luci build-ipk \
	docker-apk docker-ipk shell shell-ipk \
	lint go-build go-test go-lint go-fmt clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "  TARGET=$(TARGET)  GOARCH=$(GOARCH)  PKG_PLATFORM=$(PKG_PLATFORM)"
	@echo "  Override: TARGET=ath79/generic make build-core"

# ── Core package (Go cross-compile + local packaging) ────────

define go-cross-compile
	@mkdir -p $(OUTPUT_DIR)
	cd $(GO_PKG_DIR) && GOOS=linux GOARCH=$(GOARCH) GOARM=$(GOARM) GOMIPS=$(GOMIPS) \
		go build -trimpath -ldflags='-s -w -X main.version=$(PKG_VERSION)' -o ../$(OUTPUT_DIR)/$(GO_BIN) ./cmd/vpn-manager
endef

build-core: ## Build horn-vpn-manager .apk (single platform)
	$(go-cross-compile)
	PKG_VERSION=$(PKG_VERSION) PKG_RELEASE=$(PKG_RELEASE) PKG_ARCH=$(PKG_ARCH) PKG_PLATFORM=$(PKG_PLATFORM) \
		./scripts/package-apk.sh $(OUTPUT_DIR)/$(GO_BIN) $(GO_PKG_DIR)/files $(OUTPUT_DIR)
	@rm -f $(OUTPUT_DIR)/$(GO_BIN)

build-ipk-core: ## Build horn-vpn-manager .ipk (single platform)
	$(go-cross-compile)
	PKG_VERSION=$(PKG_VERSION) PKG_RELEASE=$(PKG_RELEASE) PKG_ARCH=$(PKG_ARCH) PKG_PLATFORM=$(PKG_PLATFORM) \
		./scripts/package-ipk.sh $(OUTPUT_DIR)/$(GO_BIN) $(GO_PKG_DIR)/files $(OUTPUT_DIR)
	@rm -f $(OUTPUT_DIR)/$(GO_BIN)

# ── Multi-platform core builds ──────────────────────────────

build-all: ## Build horn-vpn-manager .apk for all platforms
	@mkdir -p $(OUTPUT_DIR)
	@for plat in $(ALL_PLATFORMS); do \
		goarch=$$(echo "$$plat" | cut -d, -f1); \
		goarm=$$(echo "$$plat" | cut -d, -f2); \
		gomips=$$(echo "$$plat" | cut -d, -f3); \
		pkgarch=$$(echo "$$plat" | cut -d, -f4); \
		label=$$(echo "$$plat" | cut -d, -f5); \
		echo ""; \
		echo "========== $$label =========="; \
		(cd $(GO_PKG_DIR) && GOOS=linux GOARCH=$$goarch GOARM=$$goarm GOMIPS=$$gomips \
			go build -trimpath -ldflags="-s -w -X main.version=$(PKG_VERSION)" -o ../$(OUTPUT_DIR)/$(GO_BIN) ./cmd/vpn-manager) && \
		PKG_VERSION=$(PKG_VERSION) PKG_RELEASE=$(PKG_RELEASE) PKG_ARCH=$$pkgarch PKG_PLATFORM=$$label \
			./scripts/package-apk.sh $(OUTPUT_DIR)/$(GO_BIN) $(GO_PKG_DIR)/files $(OUTPUT_DIR) && \
		rm -f $(OUTPUT_DIR)/$(GO_BIN) || exit 1; \
	done
	@echo ""
	@echo ">> All platforms built:"
	@ls -lh $(OUTPUT_DIR)/horn-vpn-manager-*.apk

build-ipk-all: ## Build horn-vpn-manager .ipk for all platforms
	@mkdir -p $(OUTPUT_DIR)
	@for plat in $(ALL_PLATFORMS); do \
		goarch=$$(echo "$$plat" | cut -d, -f1); \
		goarm=$$(echo "$$plat" | cut -d, -f2); \
		gomips=$$(echo "$$plat" | cut -d, -f3); \
		pkgarch=$$(echo "$$plat" | cut -d, -f4); \
		label=$$(echo "$$plat" | cut -d, -f5); \
		echo ""; \
		echo "========== $$label =========="; \
		(cd $(GO_PKG_DIR) && GOOS=linux GOARCH=$$goarch GOARM=$$goarm GOMIPS=$$gomips \
			go build -trimpath -ldflags="-s -w -X main.version=$(PKG_VERSION)" -o ../$(OUTPUT_DIR)/$(GO_BIN) ./cmd/vpn-manager) && \
		PKG_VERSION=$(PKG_VERSION) PKG_RELEASE=$(PKG_RELEASE) PKG_ARCH=$$pkgarch PKG_PLATFORM=$$label \
			./scripts/package-ipk.sh $(OUTPUT_DIR)/$(GO_BIN) $(GO_PKG_DIR)/files $(OUTPUT_DIR) && \
		rm -f $(OUTPUT_DIR)/$(GO_BIN) || exit 1; \
	done
	@echo ""
	@echo ">> All platforms built:"
	@ls -lh $(OUTPUT_DIR)/horn-vpn-manager_*.ipk

# ── LuCI package (local, no SDK needed) ─────────────────────

LUCI_SRC = horn-vpn-manager-luci

build-luci: ## Build horn-vpn-manager-luci .apk
	@mkdir -p $(OUTPUT_DIR)
	PKG_VERSION=$(PKG_VERSION) PKG_RELEASE=$(PKG_RELEASE) \
		./scripts/package-luci-apk.sh $(LUCI_SRC) $(OUTPUT_DIR)

build-ipk-luci: ## Build horn-vpn-manager-luci .ipk
	@mkdir -p $(OUTPUT_DIR)
	PKG_VERSION=$(PKG_VERSION) PKG_RELEASE=$(PKG_RELEASE) \
		./scripts/package-luci-ipk.sh $(LUCI_SRC) $(OUTPUT_DIR)

# ── Aggregates ───────────────────────────────────────────────

build: build-core build-luci ## Build .apk packages (core + luci)

build-ipk: build-ipk-core build-ipk-luci ## Build .ipk packages (core + luci)

# ── Interactive shell ─────────────────────────────────────────

shell: docker-apk ## Shell inside SNAPSHOT SDK
	docker run --rm -it --platform linux/amd64 $(VOLUMES) $(IMAGE_APK) shell

shell-ipk: docker-ipk ## Shell inside release SDK
	docker run --rm -it --platform linux/amd64 $(VOLUMES) $(IMAGE_IPK) shell

# ── Lint ──────────────────────────────────────────────────────

lint: go-fmt go-lint ## Run all checks (Go)

# ── Go development ───────────────────────────────────────────

go-build: ## Build vpn-manager binary to bin/ (native)
	@mkdir -p $(OUTPUT_DIR)
	cd $(GO_PKG_DIR) && go build -trimpath -ldflags='-s -w -X main.version=$(PKG_VERSION)' -o ../$(OUTPUT_DIR)/$(GO_BIN) ./cmd/vpn-manager

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
