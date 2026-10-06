// models_handler.go exposes the per-credential model list to the panel.
//
// # WHY THIS EXISTS
//
// The card used to show only "已支持 N 个模型" — a bare count. That answers
// nothing actionable: when a model is missing, the operator needs to know WHICH
// models the credential actually advertises before they can tell whether the
// problem is realm routing, an excluded-model filter, a config pin, or the
// upstream catalog.
//
// The list is computed by the SAME resolver the host calls during model
// discovery (fetchDynamicModelsFromStorage), so the panel cannot disagree with
// what CPA routes on. Recomputing it differently here would be the same class
// of mistake as the health bar: showing the operator a number that is not the
// number the host uses.
//
// Note on the data source: the host does not expose a "give me the registry
// entry for credential X" RPC to plugins, so this is not a cache read — it runs
// the plugin's own resolver. That is deliberate and correct here, because the
// plugin IS the authority for its own provider's model catalog.
package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// handleModelsQuery serves GET /models[?auth_index=...].
//
// With auth_index: that credential's list. Without: every account, keyed by
// auth_index, so the panel can render all cards from one request.
func handleModelsQuery(req pluginapi.ManagementRequest, contexts ...context.Context) map[string]any {
	authIndex := queryParam(req, "auth_index")
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	build := func(f pluginapi.HostAuthFileEntry) map[string]any {
		sa, _, errGet := hostAuthGetBundle(f.AuthIndex)
		if errGet != nil {
			return map[string]any{
				"auth_index": f.AuthIndex,
				"error":      "load auth: " + errGet.Error(),
			}
		}
		result := credentialModelsResponse(modelContext(contexts...), sa, false)
		result["auth_index"] = f.AuthIndex
		result["auth_id"] = f.ID
		result["name"] = f.Name
		result["region"] = panelRegion(sa)
		return result
	}

	if authIndex != "" {
		for _, f := range files {
			if f.AuthIndex == authIndex {
				return build(f)
			}
		}
		return map[string]any{"error": "auth_index not found: " + authIndex}
	}

	rows := make([]map[string]any, 0, len(files))
	for _, f := range files {
		rows = append(rows, build(f))
	}
	return map[string]any{"accounts": rows}
}

// panelModel is one model row for the panel.
type panelModel struct {
	modelDetails
	EffectiveEffort string `json:"effective_effort,omitempty"`
	ID              string `json:"id"`
	// Name is the display name when the upstream provides one.
	Name                string `json:"name,omitempty"`
	ContextLength       int64  `json:"context_length,omitempty"`
	MaxCompletionTokens int64  `json:"max_completion_tokens,omitempty"`
	Disabled            bool   `json:"disabled"`
}

// modelsForCredential resolves the models a credential advertises, using the
// same path the host triggers during discovery.
//
// Sorted by ID so the panel list is stable across refreshes — an unstable
// order makes it look like the catalog changed when it did not.
func modelsForCredential(sa *storedAuth) []panelModel {
	if sa == nil {
		return nil
	}
	raw, err := storedAuthJSON(sa)
	if err != nil {
		return nil
	}
	return panelModelsFromInfo(fetchDynamicModelsFromStorage(raw))
}

func panelModelsFromInfo(infos []pluginapi.ModelInfo) []panelModel {
	out := make([]panelModel, 0, len(infos))
	for _, m := range infos {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		out = append(out, panelModel{
			ID:                  id,
			Name:                strings.TrimSpace(m.Name),
			ContextLength:       m.ContextLength,
			MaxCompletionTokens: m.MaxCompletionTokens,
			Disabled:            isGloballyDisabledModel(id),
		})
	}
	for i := range out {
		if strings.HasPrefix(strings.ToLower(out[i].ID), "hy3") || strings.HasPrefix(strings.ToLower(out[i].ID), "hy4") {
			out[i].EffectiveEffort = "high"
		}
	}
	sortPanelModels(out)
	return out
}

// sortPanelModels orders rows by ID (insertion sort: these lists are tiny, and
// this avoids pulling in a sort dependency for a handful of items).
func sortPanelModels(models []panelModel) {
	for i := 1; i < len(models); i++ {
		for j := i; j > 0 && models[j].ID < models[j-1].ID; j-- {
			models[j], models[j-1] = models[j-1], models[j]
		}
	}
}

// storedAuthJSON re-serializes a storedAuth for the resolver, which takes raw
// JSON because the host hands it the credential file bytes.
//
// A marshal failure returns nil rather than panicking: this runs while
// rendering the panel, and a malformed credential should show an empty model
// list, not take down the dashboard.
func storedAuthJSON(sa *storedAuth) ([]byte, error) {
	if sa == nil {
		return nil, nil
	}
	raw, err := json.Marshal(sa)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// handleModelsRefresh re-runs discovery for one credential, so the panel can
// offer a manual refresh when the upstream catalog changes.
//
// Discovery failures are returned as an error string rather than swallowed:
// "the list did not change" and "the upstream refused" must look different.
func handleModelsRefresh(req pluginapi.ManagementRequest, contexts ...context.Context) map[string]any {
	authIndex := queryParam(req, "auth_index")
	if authIndex == "" {
		authIndex = requestBodyString(req, "auth_index")
	}
	if authIndex == "" {
		return map[string]any{"error": "auth_index is required"}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	for _, f := range files {
		if f.AuthIndex != authIndex {
			continue
		}
		sa, _, errGet := hostAuthGetBundle(f.AuthIndex)
		if errGet != nil {
			return map[string]any{"error": "load auth: " + errGet.Error()}
		}
		result := credentialModelsResponse(modelContext(contexts...), sa, true)
		result["auth_index"] = authIndex
		return result
	}
	return map[string]any{"error": "auth_index not found: " + authIndex}
}

// requestBodyString reads a top-level string field from a JSON request body.
func requestBodyString(req pluginapi.ManagementRequest, key string) string {
	if len(req.Body) == 0 {
		return ""
	}
	var body map[string]any
	if jsonUnmarshal(req.Body, &body) != nil {
		return ""
	}
	if v, ok := body[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// Read and refresh expose the result of THIS resolution, not another account's
// mutable realm summary. A fallback is usable metadata, not discovery success.
func credentialModelsResponse(ctx context.Context, sa *storedAuth, force bool) map[string]any {
	raw, err := storedAuthJSON(sa)
	if err != nil {
		return map[string]any{"error": "invalid stored credential"}
	}
	result := resolveCredentialModels(ctx, raw, force)
	models := panelModelsFromInfo(result.Models)
	for i := range models {
		models[i].modelDetails = result.Details[models[i].ID]
	}
	return map[string]any{"status": result.Status, "models": models, "count": len(models), "source": result.Source, "warning": result.Warning}
}
