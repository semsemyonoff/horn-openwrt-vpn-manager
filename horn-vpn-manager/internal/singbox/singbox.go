// Package singbox provides typed sing-box config rendering for the subscription pipeline.
package singbox

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// DefaultTemplatePath is the on-device path to the installed template.
const DefaultTemplatePath = "/usr/share/horn-vpn-manager/sing-box.template.json"

// SubsTagsFilename is the filename written alongside the config for LuCI tag-to-name lookup.
const SubsTagsFilename = "subs-tags.json"

const routeActionSniff = "sniff"

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
// A sing-box 1.13 compatible sniff rule is emitted first, then generated route
// rules are prepended before any remaining static template rules.
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

	if err := stripDeprecatedInboundFields(topLevel); err != nil {
		return nil, err
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

	if err := mergeOutbounds(topLevel, genOutbounds); err != nil {
		return nil, err
	}
	if err := mergeRoute(topLevel, genRules, defaultFinalTag); err != nil {
		return nil, err
	}
	overrideLogLevel(topLevel, logLevel)

	return json.MarshalIndent(topLevel, "", "  ")
}

func mergeOutbounds(topLevel map[string]json.RawMessage, genOutbounds []json.RawMessage) error {
	var staticOutbounds []json.RawMessage
	if raw, ok := topLevel["outbounds"]; ok {
		if err := json.Unmarshal(raw, &staticOutbounds); err != nil {
			return fmt.Errorf("parse template outbounds: expected array, got unexpected type: %w", err)
		}
	}

	merged := make([]json.RawMessage, 0, len(genOutbounds)+len(staticOutbounds))
	merged = append(merged, genOutbounds...)
	for _, ob := range staticOutbounds {
		if !isPlaceholder(ob) && !isLegacySpecialOutbound(ob) {
			merged = append(merged, ob)
		}
	}
	if b, err := json.Marshal(merged); err != nil {
		return fmt.Errorf("marshal outbounds: %w", err)
	} else {
		topLevel["outbounds"] = b
	}

	return nil
}

func mergeRoute(
	topLevel map[string]json.RawMessage,
	genRules []json.RawMessage,
	defaultFinalTag string,
) error {
	routeMap := make(map[string]json.RawMessage)
	if raw, ok := topLevel["route"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &routeMap); err != nil {
			return fmt.Errorf("parse template route: %w", err)
		}
		if routeMap == nil {
			return errors.New("parse template route: unexpected null value")
		}
	}

	var staticRules []json.RawMessage
	if raw, ok := routeMap["rules"]; ok {
		if err := json.Unmarshal(raw, &staticRules); err != nil {
			return fmt.Errorf("parse template route.rules: expected array, got unexpected type: %w", err)
		}
	}

	mergedRules := make([]json.RawMessage, 0, 1+len(genRules)+len(staticRules))
	mergedRules = append(mergedRules, sniffRuleRaw)
	for _, rule := range genRules {
		if !isSniffRule(rule) {
			mergedRules = append(mergedRules, rule)
		}
	}
	for _, rule := range staticRules {
		if !isPlaceholder(rule) && !isSniffRule(rule) {
			mergedRules = append(mergedRules, rule)
		}
	}

	if b, err := json.Marshal(mergedRules); err != nil {
		return fmt.Errorf("marshal route rules: %w", err)
	} else {
		routeMap["rules"] = b
	}
	if b, err := json.Marshal(defaultFinalTag); err != nil {
		return fmt.Errorf("marshal route final: %w", err)
	} else {
		routeMap["final"] = b
	}

	if b, err := json.Marshal(routeMap); err != nil {
		return fmt.Errorf("marshal route: %w", err)
	} else {
		topLevel["route"] = b
	}

	return nil
}

func overrideLogLevel(topLevel map[string]json.RawMessage, logLevel string) {
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
}

var sniffRuleRaw = json.RawMessage(`{"action":"` + routeActionSniff + `"}`)

func stripDeprecatedInboundFields(topLevel map[string]json.RawMessage) error {
	raw, ok := topLevel["inbounds"]
	if !ok {
		return nil
	}

	var inbounds []json.RawMessage
	if err := json.Unmarshal(raw, &inbounds); err != nil {
		return fmt.Errorf("parse template inbounds: expected array, got unexpected type: %w", err)
	}

	for i, inbound := range inbounds {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(inbound, &obj); err != nil {
			continue
		}
		delete(obj, "domain_strategy")
		delete(obj, "sniff")
		delete(obj, "sniff_override_destination")
		b, err := json.Marshal(obj)
		if err != nil {
			return fmt.Errorf("marshal inbound: %w", err)
		}
		inbounds[i] = b
	}

	b, err := json.Marshal(inbounds)
	if err != nil {
		return fmt.Errorf("marshal inbounds: %w", err)
	}
	topLevel["inbounds"] = b
	return nil
}

func isLegacySpecialOutbound(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	var outboundType string
	if err := json.Unmarshal(obj["type"], &outboundType); err != nil {
		return false
	}
	return outboundType == "block" || outboundType == "dns"
}

func isSniffRule(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	var action string
	if err := json.Unmarshal(obj["action"], &action); err != nil {
		return false
	}
	return action == routeActionSniff
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
