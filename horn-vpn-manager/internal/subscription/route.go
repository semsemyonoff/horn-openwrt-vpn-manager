package subscription

import "github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"

// RouteRule is a typed sing-box route rule that maps traffic matching specified
// domain suffixes or IP CIDRs to an outbound. Generated from per-subscription
// routing config and references the subscription's FinalTag.
type RouteRule struct {
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	IPCIDR       []string `json:"ip_cidr,omitempty"`
	Outbound     string   `json:"outbound"`
}

// BuildRouteRules produces a RouteRule for a subscription's manual routing
// entries (domains and ip_cidrs from the route config block). Returns nil when
// route is nil or has no actionable entries.
//
// Only non-default subscriptions should have route rules generated. The caller
// is responsible for this check. finalTag must be the FinalTag from the
// subscription's OutboundPlan so the rule points to the correct outbound.
func BuildRouteRules(route *config.SubscriptionRoute, finalTag string) *RouteRule {
	if route == nil {
		return nil
	}
	rule := &RouteRule{Outbound: finalTag}
	if len(route.Domains) > 0 {
		rule.DomainSuffix = make([]string, len(route.Domains))
		copy(rule.DomainSuffix, route.Domains)
	}
	if len(route.IPCIDRs) > 0 {
		rule.IPCIDR = make([]string, len(route.IPCIDRs))
		copy(rule.IPCIDR, route.IPCIDRs)
	}
	if len(rule.DomainSuffix) == 0 && len(rule.IPCIDR) == 0 {
		return nil
	}
	return rule
}
