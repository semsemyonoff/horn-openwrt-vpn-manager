// Package singbox provides typed sing-box config rendering for the subscription pipeline.
package singbox

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

// DefaultTemplatePath is the on-device path to the installed template.
const DefaultTemplatePath = "/usr/share/horn-vpn-manager/sing-box.template.json"

// SubsTagsFilename is the filename written alongside the config for LuCI tag-to-name lookup.
const SubsTagsFilename = "subs-tags.json"

//go:embed sing-box.template.default.json
var embeddedTemplate []byte

// LoadTemplate reads the template from path. If path is empty, the embedded
// default template is returned.
func LoadTemplate(path string) ([]byte, error) {
	if path == "" {
		return embeddedTemplate, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template %q: %w", path, err)
	}
	return data, nil
}

// RenderConfig produces the final sing-box config JSON by merging the template
// with generated outbounds and route rules from all processed subscriptions.
//
// Generated outbounds are prepended before any static template outbounds.
// Generated route rules are prepended before any static template rules.
// Placeholder strings (e.g. "__VLESS_OUTBOUNDS__") in template arrays are stripped.
// route.final is set to defaultFinalTag.
// log.level is overridden with logLevel when non-empty.
func RenderConfig(
	templateData []byte,
	outbounds []any,
	routeRules []any,
	defaultFinalTag string,
	logLevel string,
) ([]byte, error) {
	// Parse the template into a top-level map so that any unknown top-level
	// sing-box keys (certificate, endpoints, etc.) are preserved verbatim.
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(templateData, &topLevel); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	if topLevel == nil {
		topLevel = make(map[string]json.RawMessage)
	}

	// Serialize generated outbounds.
	genOutbounds, err := marshalAny(outbounds)
	if err != nil {
		return nil, fmt.Errorf("marshal outbounds: %w", err)
	}

	// Serialize generated route rules.
	genRules, err := marshalAny(routeRules)
	if err != nil {
		return nil, fmt.Errorf("marshal route rules: %w", err)
	}

	// Merge outbounds: generated first, then non-placeholder static outbounds.
	var staticOutbounds []json.RawMessage
	if raw, ok := topLevel["outbounds"]; ok {
		if err := json.Unmarshal(raw, &staticOutbounds); err != nil {
			return nil, fmt.Errorf("parse template outbounds: expected array, got unexpected type: %w", err)
		}
	}
	merged := make([]json.RawMessage, 0, len(genOutbounds)+len(staticOutbounds))
	merged = append(merged, genOutbounds...)
	for _, ob := range staticOutbounds {
		if !isPlaceholder(ob) {
			merged = append(merged, ob)
		}
	}
	if b, err := json.Marshal(merged); err != nil {
		return nil, fmt.Errorf("marshal outbounds: %w", err)
	} else {
		topLevel["outbounds"] = b
	}

	// Merge route rules: decode the template route as a raw map to preserve
	// any fields we do not model (geoip, geosite, etc.), then override only
	// the fields we manage (rules, final).
	routeMap := make(map[string]json.RawMessage)
	if raw, ok := topLevel["route"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &routeMap); err != nil {
			return nil, fmt.Errorf("parse template route: %w", err)
		}
		if routeMap == nil {
			return nil, fmt.Errorf("parse template route: unexpected null value")
		}
	}

	// Extract existing static rules from the map to merge with generated ones.
	var staticRules []json.RawMessage
	if raw, ok := routeMap["rules"]; ok {
		if err := json.Unmarshal(raw, &staticRules); err != nil {
			return nil, fmt.Errorf("parse template route.rules: expected array, got unexpected type: %w", err)
		}
	}
	mergedRules := make([]json.RawMessage, 0, len(genRules)+len(staticRules))
	mergedRules = append(mergedRules, genRules...)
	for _, rule := range staticRules {
		if !isPlaceholder(rule) {
			mergedRules = append(mergedRules, rule)
		}
	}

	// Write back the managed fields into the route map.
	if b, err := json.Marshal(mergedRules); err == nil {
		routeMap["rules"] = b
	}
	if b, err := json.Marshal(defaultFinalTag); err == nil {
		routeMap["final"] = b
	}

	if b, err := json.Marshal(routeMap); err != nil {
		return nil, fmt.Errorf("marshal route: %w", err)
	} else {
		topLevel["route"] = b
	}

	// Override log level when provided, preserving other log fields from the template.
	if logLevel != "" {
		var logMap map[string]any
		if raw, ok := topLevel["log"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &logMap)
		}
		if logMap == nil {
			logMap = make(map[string]any)
		}
		logMap["level"] = logLevel
		if b, err := json.Marshal(logMap); err == nil {
			topLevel["log"] = b
		}
	}

	return json.MarshalIndent(topLevel, "", "  ")
}

// isPlaceholder reports whether a raw JSON value is a bare string (placeholder).
func isPlaceholder(raw json.RawMessage) bool {
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == '"'
	}
	return false
}

// marshalAny serializes a slice of arbitrary values to raw JSON messages.
func marshalAny(items []any) ([]json.RawMessage, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
