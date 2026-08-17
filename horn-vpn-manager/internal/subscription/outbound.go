package subscription

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/nodes"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/proto"
)

const (
	defaultInterval  = "5m"
	defaultTolerance = 100

	protoVLESS = "vless"

	transportTCP  = "tcp"
	transportGRPC = "grpc"

	securityTLS     = "tls"
	securityReality = "reality"
)

// OutboundPlan holds the sing-box outbound configuration generated for a single
// subscription. It covers both node outbounds and group outbounds.
type OutboundPlan struct {
	// ID is the subscription key this plan was generated from. Fallback chains
	// reference subscriptions by id, so plans must stay resolvable by it.
	ID string

	// NodeOutbounds holds individual node outbounds, one per surviving node, in
	// the protocol-specific struct the owning package produced.
	// Single-node: one entry tagged "<id>-single".
	// Multi-node: entries tagged "<id>-node-<hash>".
	//
	// The element type is any because each protocol owns its own outbound
	// struct. Read tags from NodeTags rather than reflecting on the values.
	NodeOutbounds []any

	// NodeTags holds the tag of each NodeOutbounds entry, in the same order.
	// The tags are not readable off the outbounds themselves once they are
	// opaque, and callers (logging, groups) need them.
	NodeTags []string

	// URLTestGroup is the auto-select group for multi-node subscriptions.
	// Nil for single-node subscriptions.
	URLTestGroup *URLTestOutbound

	// SelectorGroup is the manual-select group for multi-node subscriptions.
	// Nil for single-node subscriptions.
	SelectorGroup *SelectorOutbound

	// FallbackGroup is the chain group generated when the subscription declares
	// a fallback. Nil otherwise. When set it supersedes FinalTag as the tag
	// route.final (default subscription) or the route rules point at.
	FallbackGroup *FallbackOutbound

	// FinalTag is the routing outbound tag to use in route rules:
	// "<id>-single" for single-node, "<id>-manual" for multi-node.
	FinalTag string

	// TagNames maps each generated tag to its display name. Useful for
	// UI integration (e.g., future LuCI phase).
	TagNames map[string]string

	// RouteRules holds the sing-box route rules for this subscription's manual
	// routing entries (domains and IP CIDRs). Nil for default subscriptions
	// and for subscriptions with no route config. Domains and IP CIDRs are
	// stored as separate rules to preserve OR match semantics in sing-box.
	RouteRules []*RouteRule
}

// URLTestOutbound is a sing-box urltest outbound group.
type URLTestOutbound struct {
	Type                      string   `json:"type"`
	Tag                       string   `json:"tag"`
	Outbounds                 []string `json:"outbounds"`
	URL                       string   `json:"url"`
	Interval                  string   `json:"interval"`
	Tolerance                 int      `json:"tolerance"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections"`
}

// SelectorOutbound is a sing-box selector outbound group.
type SelectorOutbound struct {
	Type                      string   `json:"type"`
	Tag                       string   `json:"tag"`
	Outbounds                 []string `json:"outbounds"`
	Default                   string   `json:"default"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections"`
}

// FallbackOutbound is a sing-box fallback outbound group: connections try each
// outbound in order and an outbound that fails to dial is skipped for
// BlacklistTimeout. Unlike urltest it reacts to dial failure rather than probe
// latency, which is the only way to leave a node that still answers probes but
// stalls under load.
//
// fallback is not an upstream sing-box outbound type — it exists only in the
// extended build. A stock build rejects it at `sing-box check` time with
// "unknown outbound type", which system.ApplySingbox surfaces before the live
// config is replaced.
//
// The field set is exactly FallbackOutboundOptions in the extended build
// (option/group.go): outbounds and blacklist_timeout, nothing else. sing-box
// decodes outbound options with unknown fields disallowed, so an extra key —
// interrupt_exist_connections, which selector and urltest do accept — makes
// `sing-box check` reject the whole config.
type FallbackOutbound struct {
	Type             string   `json:"type"`
	Tag              string   `json:"tag"`
	Outbounds        []string `json:"outbounds"`
	BlacklistTimeout string   `json:"blacklist_timeout,omitempty"`
}

// fallbackTag returns the generated fallback group tag for a subscription id.
func fallbackTag(id string) string { return id + "-fallback" }

// BuildOptions carries the non-identifying inputs of BuildOutbounds. They are
// grouped rather than passed positionally because Interval and ConnectTimeout
// are adjacent duration strings and would be trivial to transpose.
type BuildOptions struct {
	// Interval is the urltest probe interval; empty means defaultInterval.
	Interval string
	// Tolerance is the urltest switch tolerance in ms; zero means defaultTolerance.
	Tolerance int
	// TestURL is the urltest probe URL.
	TestURL string
	// ConnectTimeout is set on every node outbound; empty omits the field.
	ConnectTimeout string
}

// BuildOutbounds generates the sing-box outbound configuration for a subscription
// from its node URIs, of any scheme the nodes dispatcher supports. The id
// parameter is the stable subscription key used to derive outbound tags.
//
// For a single node, one outbound is produced with tag "<id>-single".
// For multiple nodes, per-node outbounds tagged "<id>-node-<hash>" are produced
// alongside a urltest group "<id>-auto" and a selector group "<id>-manual".
// Groups reference their members by tag only, so a subscription mixing
// protocols yields one shared urltest/selector pair like any other.
func BuildOutbounds(id string, uris []string, opts BuildOptions) (*OutboundPlan, error) {
	if len(uris) == 0 {
		return nil, fmt.Errorf("no URIs for subscription %q", id)
	}

	// Apply defaults matching legacy shell behavior.
	interval := opts.Interval
	if interval == "" {
		interval = defaultInterval
	}
	tolerance := opts.Tolerance
	if tolerance == 0 {
		tolerance = defaultTolerance
	}

	plan := &OutboundPlan{
		ID:       id,
		TagNames: make(map[string]string),
	}

	parsed := make([]proto.Node, 0, len(uris))
	for _, u := range uris {
		n, err := nodes.Parse(u)
		if err != nil {
			logx.Warn("skipping unparseable node URI: %v", err)
			continue
		}
		parsed = append(parsed, n)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("no valid nodes found in subscription %q (supported schemes: %s)",
			id, strings.Join(nodes.Schemes(), ", "))
	}

	if len(parsed) == 1 {
		// Single-node mode: use <id>-single tag directly.
		tag := id + "-single"
		plan.NodeOutbounds = append(plan.NodeOutbounds, parsed[0].Outbound(tag, opts.ConnectTimeout))
		plan.NodeTags = append(plan.NodeTags, tag)
		plan.FinalTag = tag
		plan.TagNames[tag] = parsed[0].Name()
	} else {
		// Multi-node mode: hash-tagged nodes + urltest + selector.
		//
		// Providers routinely repeat the same endpoint across a subscription;
		// duplicates cost an extra urltest probe each and add nothing. Dedup
		// runs here rather than before the single/multi split so that an
		// all-duplicate subscription keeps its "<id>-manual" FinalTag instead of
		// silently collapsing to "<id>-single" and moving route.final.
		//
		// The dedup key is the marshalled outbound, not StableHash: the hash
		// omits ALPN, Mode and HeaderType, each of which changes the rendered
		// outbound. Tags still come from StableHash so subs-tags.json, saved
		// selector choices and experimental.cache_file state stay valid — which
		// is also why the collision suffix below has to stay: two distinct nodes
		// can share a hash.
		nodeTags := make([]string, 0, len(parsed))
		seenTags := make(map[string]int, len(parsed))
		seenOutbounds := make(map[string]struct{}, len(parsed))
		duplicates := 0
		for _, n := range parsed {
			// The tagless outbound is the dedup key; the tagged one is what the
			// plan stores. Building both is what lets the outbound stay opaque:
			// there is no Tag field to assign after the fact.
			key, err := json.Marshal(n.Outbound("", opts.ConnectTimeout))
			if err != nil {
				return nil, fmt.Errorf("marshalling outbound for subscription %q: %w", id, err)
			}
			base := fmt.Sprintf("%s-node-%s", id, n.StableHash())
			// The suffix counter advances for skipped duplicates too, so dedup
			// never renames a surviving node: without this, dropping a duplicate
			// would shift the next colliding node from "-3" to "-2", silently
			// repointing a tag that saved selector choices and
			// experimental.cache_file state still refer to.
			count := seenTags[base]
			seenTags[base]++
			if _, seen := seenOutbounds[string(key)]; seen {
				duplicates++
				continue
			}
			seenOutbounds[string(key)] = struct{}{}

			tag := base
			if count > 0 {
				tag = fmt.Sprintf("%s-%d", base, count+1)
			}
			plan.NodeOutbounds = append(plan.NodeOutbounds, n.Outbound(tag, opts.ConnectTimeout))
			plan.TagNames[tag] = n.Name()
			nodeTags = append(nodeTags, tag)
		}
		plan.NodeTags = nodeTags
		if duplicates > 0 {
			logx.Detail("  Subscription %s: skipped %d duplicate nodes", id, duplicates)
		}

		autoTag := id + "-auto"
		manualTag := id + "-manual"

		// interrupt_exist_connections tears down connections to the previously
		// selected node when a group re-selects; without it they hang on a node
		// that has already stopped answering. On urltest it also fires on benign
		// latency-driven re-selection, so the per-subscription tolerance is what
		// keeps re-selection tied to genuine degradation.
		plan.URLTestGroup = &URLTestOutbound{
			Type:                      "urltest",
			Tag:                       autoTag,
			Outbounds:                 nodeTags,
			URL:                       opts.TestURL,
			Interval:                  interval,
			Tolerance:                 tolerance,
			InterruptExistConnections: true,
		}
		plan.TagNames[autoTag] = "Auto"

		manualOutbounds := make([]string, 0, len(nodeTags)+1)
		manualOutbounds = append(manualOutbounds, autoTag)
		manualOutbounds = append(manualOutbounds, nodeTags...)
		plan.SelectorGroup = &SelectorOutbound{
			Type:                      "selector",
			Tag:                       manualTag,
			Outbounds:                 manualOutbounds,
			Default:                   autoTag,
			InterruptExistConnections: true,
		}
		plan.TagNames[manualTag] = id

		plan.FinalTag = manualTag
	}

	return plan, nil
}
