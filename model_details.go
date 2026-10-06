package main

import (
	"encoding/json"
	"strings"
)

type modelDetails struct {
	Credits        string   `json:"credits,omitempty"`
	Vendor         string   `json:"vendor,omitempty"`
	Efforts        []string `json:"efforts,omitempty"`
	DefaultEffort  string   `json:"default_effort,omitempty"`
	SupportsImages *bool    `json:"supports_images,omitempty"`
	OnlyReasoning  bool     `json:"only_reasoning,omitempty"`
	Source         string   `json:"metadata_source,omitempty"`
}
type modelDetailsKey struct{}
type modelDetailsCollector struct{ Rows map[string]modelDetails }

func detailsFromDiscovery(models []discoveredModel) map[string]modelDetails {
	out := map[string]modelDetails{}
	for _, m := range models {
		var reasoning struct {
			Efforts []string `json:"supportedEfforts"`
		}
		_ = json.Unmarshal(m.Reasoning, &reasoning)
		efforts := []string{}
		seen := map[string]bool{}
		for _, e := range reasoning.Efforts {
			e = strings.TrimSpace(e)
			if e != "" && !seen[e] {
				efforts = append(efforts, e)
				seen[e] = true
			}
		}
		images := m.ImageDeclaration
		if m.DisabledMultimodal {
			disabled := false
			images = &disabled
		}
		out[m.ID] = modelDetails{Credits: m.Credits, Vendor: m.Vendor, Efforts: efforts, DefaultEffort: parseDefaultEffort(m.Reasoning), SupportsImages: images, OnlyReasoning: m.OnlyReasoning, Source: "upstream"}
	}
	return out
}
