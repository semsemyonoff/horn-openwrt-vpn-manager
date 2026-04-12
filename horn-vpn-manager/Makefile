include $(TOPDIR)/rules.mk

PKG_NAME:=horn-vpn-manager
PKG_VERSION:=2.0.0
PKG_RELEASE:=1

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1

GO_PKG:=github.com/semsemyonoff/horn-openwrt-vpn-manager
GO_PKG_BUILD_PKG:=$(GO_PKG)/cmd/vpn-manager

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/horn-vpn-manager
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=VPN
  TITLE:=VPN subscription manager for sing-box
  DEPENDS:=+sing-box +dnsmasq-full
endef

define Package/horn-vpn-manager/description
  Manages VPN subscriptions for sing-box on OpenWrt.
  Downloads VLESS URIs, generates sing-box config with per-subscription
  outbound groups and routing rules. Includes domain/IP list management
  for dnsmasq-based VPN routing.
endef

define Package/horn-vpn-manager/conffiles
/etc/horn-vpn-manager/config.json
/etc/horn-vpn-manager/lists/manual-ip.lst
endef

define Package/horn-vpn-manager/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/vpn-manager $(1)/usr/bin/vpn-manager

	$(INSTALL_DIR) $(1)/usr/share/horn-vpn-manager
	$(INSTALL_DATA) ./files/sing-box.template.default.json $(1)/usr/share/horn-vpn-manager/sing-box.template.default.json
	$(INSTALL_DATA) ./files/config.example.json $(1)/usr/share/horn-vpn-manager/config.example.json

	$(INSTALL_DIR) $(1)/etc/horn-vpn-manager/lists

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/horn-vpn-manager.init $(1)/etc/init.d/horn-vpn-manager
endef

$(eval $(call GoBinPackage,horn-vpn-manager))
$(eval $(call BuildPackage,horn-vpn-manager))
