// Package singbox provides typed sing-box config rendering for the subscription pipeline.
package singbox

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

// DefaultTemplatePath is the on-device path to the installed default template.
const DefaultTemplatePath = "/usr/share/horn-vpn-manager/sing-box.template.default.json"

// SubsTagsFilename is the filename written alongside the config for LuCI tag-to-name lookup.
const SubsTagsFilename = "subs-tags.json"

//go:embed sing-box.template.default.json
var embeddedTemplate []byte

// rawConfig is the intermediate form used to unmarshal, merge, and re-marshal
// the final sing-box configuration.
type rawConfig struct {
	Log          json.RawMessage   `json:"log,omitempty"`
	DNS          json.RawMessage   `json:"dns,omitempty"`
	NTP          json.RawMessage   `json:"ntp,omitempty"`
	Inbounds     []json.RawMessage `json:"inbounds,omitempty"`
	Outbounds    []json.RawMessage `json:"outbounds,omitempty"`
	Route        *rawRoute         `json:"route,omitempty"`
	Experimental json.RawMessage   `json:"experimental,omitempty"`
}

type rawRoute struct {
	Rules               []json.RawMessage `json:"rules,omitempty"`
	Final               string            `json:"final,omitempty"`
	AutoDetectInterface bool              `json:"auto_detect_interface,omitempty"`
}

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
	var tmpl rawConfig
	if err := json.Unmarshal(templateData, &tmpl); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
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
	merged := make([]json.RawMessage, 0, len(genOutbounds)+len(tmpl.Outbounds))
	merged = append(merged, genOutbounds...)
	for _, ob := range tmpl.Outbounds {
		if !isPlaceholder(ob) {
			merged = append(merged, ob)
		}
	}
	tmpl.Outbounds = merged

	// Merge route rules: generated first, then non-placeholder static rules.
	if tmpl.Route == nil {
		tmpl.Route = &rawRoute{}
	}
	mergedRules := make([]json.RawMessage, 0, len(genRules)+len(tmpl.Route.Rules))
	mergedRules = append(mergedRules, genRules...)
	for _, rule := range tmpl.Route.Rules {
		if !isPlaceholder(rule) {
			mergedRules = append(mergedRules, rule)
		}
	}
	tmpl.Route.Rules = mergedRules

	// Set route.final to the default subscription's outbound tag.
	tmpl.Route.Final = defaultFinalTag

	// Override log level when provided, preserving other log fields from the template.
	if logLevel != "" {
		var logMap map[string]any
		if len(tmpl.Log) > 0 {
			_ = json.Unmarshal(tmpl.Log, &logMap)
		}
		if logMap == nil {
			logMap = make(map[string]any)
		}
		logMap["level"] = logLevel
		if b, err := json.Marshal(logMap); err == nil {
			tmpl.Log = b
		}
	}

	return json.MarshalIndent(tmpl, "", "  ")
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
