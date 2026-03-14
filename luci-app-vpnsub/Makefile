include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-vpnsub
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

LUCI_TITLE:=LuCI interface for sing-box VPN subscription manager
LUCI_DEPENDS:=+luci-base +jq +curl
LUCI_PKGARCH:=all

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)

include $(TOPDIR)/feeds/luci/luci.mk

define Package/luci-app-vpnsub/install
	$(CP) ./root/. $(1)/
	$(INSTALL_DIR) $(1)/etc/horn-vpn-manager
	$(INSTALL_BIN) $(CURDIR)/../getdomains.sh $(1)/etc/horn-vpn-manager/getdomains.sh
endef

# call BuildPackage - OpenWrt buildroot invokes this
$(eval $(call BuildPackage,luci-app-vpnsub))
