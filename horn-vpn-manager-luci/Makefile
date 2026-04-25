include $(TOPDIR)/rules.mk

PKG_NAME:=horn-vpn-manager-luci
PKG_VERSION:=2.2.1
PKG_RELEASE:=1

include $(INCLUDE_DIR)/package.mk

define Package/horn-vpn-manager-luci
  SECTION:=luci
  CATEGORY:=LuCI
  SUBMENU:=3. Applications
  TITLE:=LuCI interface for horn-vpn-manager
  DEPENDS:=+horn-vpn-manager
  PKGARCH:=all
endef

define Package/horn-vpn-manager-luci/description
  Web interface (LuCI) for managing VPN subscriptions via horn-vpn-manager.
endef

define Build/Configure
endef

define Build/Compile
	mkdir -p $(PKG_BUILD_DIR)/i18n
	python3 ./tools/po2lmo.py ./po/en/horn-vpn-manager.po $(PKG_BUILD_DIR)/i18n/horn-vpn-manager.en.lmo
	python3 ./tools/po2lmo.py ./po/ru/horn-vpn-manager.po $(PKG_BUILD_DIR)/i18n/horn-vpn-manager.ru.lmo
endef

define Package/horn-vpn-manager-luci/install
	$(CP) ./root/. $(1)/
	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./root/usr/libexec/rpcd/horn-vpn-manager $(1)/usr/libexec/rpcd/horn-vpn-manager
	$(INSTALL_DIR) $(1)/usr/lib/lua/luci/i18n
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/i18n/*.lmo $(1)/usr/lib/lua/luci/i18n/
endef

define Package/horn-vpn-manager-luci/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || /etc/init.d/rpcd restart
endef

$(eval $(call BuildPackage,horn-vpn-manager-luci))
