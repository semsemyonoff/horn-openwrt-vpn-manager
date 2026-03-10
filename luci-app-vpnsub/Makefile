include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-vpnsub
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

LUCI_TITLE:=LuCI interface for sing-box VPN subscription manager
LUCI_DEPENDS:=+luci-base +jq
LUCI_PKGARCH:=all

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot invokes this
$(eval $(call BuildPackage,luci-app-vpnsub))
