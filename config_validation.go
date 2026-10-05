package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateLifecycleConfig runs before any registration/reconfiguration side
// effects. It does not persist, merge or rewrite host-owned configuration.
// Errors deliberately omit raw values and YAML parser excerpts (which may
// contain secrets). Advanced/unknown fields retain their existing semantics.
func validateLifecycleConfig(raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var req struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid lifecycle configuration envelope")
	}
	if len(bytes.TrimSpace(req.ConfigYAML)) == 0 {
		return nil
	}
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(req.ConfigYAML))
	if err := decoder.Decode(&root); err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("config_yaml must contain valid YAML")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("config_yaml must contain exactly one YAML document")
	}
	if len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	if doc.Tag == "!!null" {
		return nil
	}
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("config_yaml must be a mapping")
	}
	// Reject duplicate keys instead of silently letting one native setting win.
	// Do not reject unrelated, opaque advanced values.
	cfg := findWorkBuddyConfigNode(&root)
	if cfg == nil {
		return nil
	}
	seen := make(map[string]bool)
	enums := map[string][]string{
		"login_region":   {"cn", "intl", "海外", "国际"},
		"login_platform": {"cli", "ide", "ide模式"},
		"scheduler_mode": {schedulerModeHost, schedulerModeBuiltin, schedulerModeOff, schedulerModeCredits},
	}
	for i := 0; i+1 < len(cfg.Content); i += 2 {
		key := strings.ToLower(strings.TrimSpace(cfg.Content[i].Value))
		if seen[key] {
			return fmt.Errorf("plugin configuration contains duplicate keys")
		}
		seen[key] = true
		allowed, native := enums[key]
		if !native {
			continue
		}
		node := cfg.Content[i+1]
		if node.Tag == "!!null" {
			continue
		} // cleared field uses the legacy default
		if node.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s must be a scalar enum value", key)
		}
		value := strings.ToLower(strings.TrimSpace(node.Value))
		if value == "" {
			continue
		} // preserve existing empty-field default
		valid := false
		for _, candidate := range allowed {
			if value == candidate {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("%s has an unsupported value", key)
		}
	}
	return nil
}
