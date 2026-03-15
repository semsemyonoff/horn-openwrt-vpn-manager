include $(TOPDIR)/rules.mk

PKG_NAME:=horn-vpn-manager-luci
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

include $(INCLUDE_DIR)/package.mk

define Package/horn-vpn-manager-luci
  SECTION:=luci
  CATEGORY:=LuCI
  SUBMENU:=3. Applications
  TITLE:=LuCI interface for horn-vpn-manager
  # Runtime: luci-base, horn-vpn-manager (not enforced at build time)
  PKGARCH:=all
endef

define Package/horn-vpn-manager-luci/description
  Web interface (LuCI) for managing VPN subscriptions via horn-vpn-manager.
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/horn-vpn-manager-luci/install
	$(CP) ./root/. $(1)/
	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./root/usr/libexec/rpcd/horn-vpn-manager $(1)/usr/libexec/rpcd/horn-vpn-manager
endef

define Package/horn-vpn-manager-luci/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || /etc/init.d/rpcd restart
endef

$(eval $(call BuildPackage,horn-vpn-manager-luci))
