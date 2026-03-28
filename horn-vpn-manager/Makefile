include $(TOPDIR)/rules.mk

PKG_NAME:=horn-vpn-manager
PKG_VERSION:=1.1.0
PKG_RELEASE:=1

include $(INCLUDE_DIR)/package.mk

define Package/horn-vpn-manager
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=VPN
  TITLE:=VPN subscription manager for sing-box
  # Runtime: jq, curl, sing-box, dnsmasq-full (not enforced at build time)
  PKGARCH:=all
endef

define Package/horn-vpn-manager/description
  Manages VPN subscriptions for sing-box on OpenWrt.
  Downloads VLESS URIs, generates sing-box config with per-subscription
  outbound groups and routing rules. Includes domain/IP list management
  for dnsmasq-based VPN routing.
endef

define Package/horn-vpn-manager/conffiles
/etc/horn-vpn-manager/subs.json
/etc/horn-vpn-manager/config.template.json
/etc/horn-vpn-manager/domains.json
/etc/horn-vpn-manager/lists/manual-ip.lst
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/horn-vpn-manager/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/vpn-manager.sh $(1)/usr/bin/vpn-manager

	$(INSTALL_DIR) $(1)/usr/libexec/horn-vpn-manager
	$(INSTALL_BIN) ./files/subs.sh $(1)/usr/libexec/horn-vpn-manager/subs.sh
	$(INSTALL_BIN) ./files/getdomains.sh $(1)/usr/libexec/horn-vpn-manager/getdomains.sh

	$(INSTALL_DIR) $(1)/usr/share/horn-vpn-manager
	$(INSTALL_DATA) ./files/config.template.json $(1)/usr/share/horn-vpn-manager/config.template.default.json
	$(INSTALL_DATA) ./files/subs.example.json $(1)/usr/share/horn-vpn-manager/subs.example.json
	$(INSTALL_DATA) ./files/domains.example.json $(1)/usr/share/horn-vpn-manager/domains.example.json

	$(INSTALL_DIR) $(1)/etc/horn-vpn-manager/lists
	$(INSTALL_CONF) ./files/config.template.json $(1)/etc/horn-vpn-manager/config.template.json

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/horn-vpn-manager.init $(1)/etc/init.d/horn-vpn-manager

	$(INSTALL_DIR) $(1)/etc/crontabs
endef

$(eval $(call BuildPackage,horn-vpn-manager))
