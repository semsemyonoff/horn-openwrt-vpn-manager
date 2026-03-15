FROM debian:bookworm-slim

ARG SDK_BASE_URL=https://downloads.openwrt.org/snapshots/targets/x86/64

# Build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates curl file gawk gettext \
    git libncurses-dev libssl-dev python3 python3-setuptools \
    rsync unzip wget xz-utils zstd && \
    rm -rf /var/lib/apt/lists/*

# OpenWrt SDK requires a non-root user
RUN useradd -m -s /bin/bash builder
USER builder
WORKDIR /home/builder

# Download and extract OpenWrt SDK (auto-detect filename and archive format)
RUN SDK_FILE=$(curl -sf "${SDK_BASE_URL}/sha256sums" \
        | grep 'openwrt-sdk-' | grep 'Linux-x86_64' \
        | awk '{print $2}' | tr -d '*') && \
    echo "Downloading SDK: ${SDK_FILE}" && \
    curl -fSL "${SDK_BASE_URL}/${SDK_FILE}" -o /tmp/sdk.tar && \
    case "${SDK_FILE}" in \
        *.tar.zst) tar --zstd -xf /tmp/sdk.tar ;; \
        *.tar.xz)  tar -xJf /tmp/sdk.tar ;; \
        *.tar.gz)  tar -xzf /tmp/sdk.tar ;; \
        *)         tar -xf /tmp/sdk.tar ;; \
    esac && \
    rm /tmp/sdk.tar && \
    mv openwrt-sdk-* sdk

WORKDIR /home/builder/sdk
ENV TERM=xterm

# Set up feeds (LuCI feed is required for horn-vpn-manager-luci)
RUN ./scripts/feeds update -a && \
    ./scripts/feeds install -a

# Prepare directories for source packages
RUN mkdir -p package/horn-vpn-manager package/horn-vpn-manager-luci

COPY --chown=builder:builder docker/entrypoint.sh /home/builder/entrypoint.sh
RUN chmod +x /home/builder/entrypoint.sh

ENTRYPOINT ["/home/builder/entrypoint.sh"]
CMD ["all"]
