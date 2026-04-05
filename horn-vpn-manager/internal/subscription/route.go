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

// BuildRouteRules produces route rules for a subscription's manual routing
// entries (domains and ip_cidrs from the route config block). Returns nil when
// route is nil or has no actionable entries.
//
// Domains and IP CIDRs are emitted as separate rules because sing-box applies
// AND semantics within a single rule: a rule with both domain_suffix and ip_cidr
// would only match traffic satisfying both conditions simultaneously, which is
// never the intended behaviour. Two rules with the same outbound correctly
// implement OR semantics.
//
// Only non-default subscriptions should have route rules generated. The caller
// is responsible for this check. finalTag must be the FinalTag from the
// subscription's OutboundPlan so the rule points to the correct outbound.
func BuildRouteRules(route *config.SubscriptionRoute, finalTag string) []*RouteRule {
	if route == nil {
		return nil
	}
	var rules []*RouteRule
	if len(route.Domains) > 0 {
		r := &RouteRule{Outbound: finalTag}
		r.DomainSuffix = make([]string, len(route.Domains))
		copy(r.DomainSuffix, route.Domains)
		rules = append(rules, r)
	}
	if len(route.IPCIDRs) > 0 {
		r := &RouteRule{Outbound: finalTag}
		r.IPCIDR = make([]string, len(route.IPCIDRs))
		copy(r.IPCIDR, route.IPCIDRs)
		rules = append(rules, r)
	}
	return rules
}
