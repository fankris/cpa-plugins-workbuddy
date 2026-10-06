package main

// Model hub joins observations, never CPA routing decisions. One directory
// account per service, one row per exact upstream ID, full per-source variants.
import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var hubChannels = []string{regionCN, regionGlobal, regionIntl}

type hubAccount struct {
	ID        string `json:"auth_index"`
	Name      string `json:"name"`
	Selected  bool   `json:"selected"`
	Available bool   `json:"available"`
}
type hubSource struct {
	Channel   string            `json:"channel"`
	Accounts  []hubAccount      `json:"accounts"`
	Account   string            `json:"auth_index"`
	Name      string            `json:"name"`
	Basis     string            `json:"basis"`
	Status    string            `json:"status"`
	Count     int               `json:"count"`
	FetchedAt string            `json:"fetched_at,omitempty"`
	Cached    bool              `json:"cached"`
	Warning   string            `json:"warning,omitempty"`
	Endpoints []directorySource `json:"endpoints"`
}
type hubVariant struct {
	Origin    string         `json:"origin"`
	Channel   string         `json:"channel"`
	Account   string         `json:"auth_index,omitempty"`
	ConfigKey string         `json:"config_key,omitempty"`
	Model     directoryModel `json:"model"`
}
type hubModel struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Origins         []string     `json:"origins"`
	DynamicChannels []string     `json:"dynamic_channels"`
	CustomChannels  []string     `json:"custom_channels"`
	Disabled        bool         `json:"disabled"`
	Variants        []hubVariant `json:"variants"`
}
type hubCredential struct {
	File      pluginapi.HostAuthFileEntry
	Auth      *storedAuth
	Channel   string
	Available bool
}

func chooseHubAccount(accounts []hubAccount, requested string) (hubAccount, string) {
	if requested != "" {
		for _, a := range accounts {
			if a.ID == requested && a.Available {
				return a, "manual"
			}
		}
		return hubAccount{}, "invalid_account"
	}
	for _, a := range accounts {
		if a.Available && a.Selected {
			return a, "selected"
		}
	}
	for _, a := range accounts {
		if a.Available {
			return a, "automatic"
		}
	}
	return hubAccount{}, "no_account"
}
func customHubVariants() []hubVariant {
	customStaticModels.RLock()
	custom := append([]customStaticModel(nil), customStaticModels.models...)
	customStaticModels.RUnlock()
	out := []hubVariant{}
	for _, channel := range hubChannels {
		for _, m := range custom {
			if !customModelChannelMatches(m.Channel, channel) || strings.TrimSpace(m.ID) == "" {
				continue
			}
			family, derived := directoryFamily(m.ID, "")
			name := m.Name
			if name == "" {
				name = m.ID
			}
			out = append(out, hubVariant{Origin: "custom", Channel: channel, ConfigKey: "models", Model: directoryModel{panelModel: panelModel{ID: m.ID, Name: name, ContextLength: m.Context, MaxCompletionTokens: m.MaxTokens, Disabled: !m.Enabled, modelDetails: modelDetails{Source: "custom"}}, Family: family, FamilyDerived: derived, RoutingStatus: "configuration"}})
		}
		// Pins are explicitly configured IDs, not dynamically observed models.
		realm := displayRegionForService(channel)
		for _, id := range pinnedModelIDsForRealm(realm) {
			if strings.TrimSpace(id) == "" {
				continue
			}
			family, derived := directoryFamily(id, "")
			key := "models_cn"
			if realm == regionIntl {
				key = "models_intl / models_global / models_intl_global"
			}
			out = append(out, hubVariant{Origin: "custom", Channel: channel, ConfigKey: key, Model: directoryModel{panelModel: panelModel{ID: id, Name: id, modelDetails: modelDetails{Source: "pin"}}, Family: family, FamilyDerived: derived, RoutingStatus: "configuration"}})
		}
	}
	return out
}
func mergeHubModels(variants []hubVariant) []hubModel {
	rows := []hubModel{}
	indexes := map[string]int{}
	add := func(xs []string, value string) []string {
		for _, x := range xs {
			if x == value {
				return xs
			}
		}
		return append(xs, value)
	}
	for _, v := range variants {
		id := strings.TrimSpace(v.Model.ID)
		if id == "" {
			continue
		}
		i, found := indexes[id]
		if !found {
			i = len(rows)
			indexes[id] = i
			name := v.Model.Name
			if name == "" {
				name = id
			}
			rows = append(rows, hubModel{ID: id, Name: name, Origins: []string{}, DynamicChannels: []string{}, CustomChannels: []string{}, Variants: []hubVariant{}, Disabled: isGloballyDisabledModel(id)})
		}
		r := &rows[i]
		r.Origins = add(r.Origins, v.Origin)
		if v.Origin == "dynamic" {
			r.DynamicChannels = add(r.DynamicChannels, v.Channel)
		} else {
			r.CustomChannels = add(r.CustomChannels, v.Channel)
		}
		r.Variants = append(r.Variants, v)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}
func handleModelHub(req pluginapi.ManagementRequest, parent context.Context, force bool) map[string]any {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": "cannot list CPA credentials"}
	}
	var body struct {
		Sources map[string]string `json:"sources"`
	}
	if len(req.Body) > 0 && json.Unmarshal(req.Body, &body) != nil {
		return map[string]any{"error": "invalid source selection"}
	}
	requested := map[string]string{}
	for _, ch := range hubChannels {
		requested[ch] = strings.TrimSpace(queryParam(req, ch))
		if body.Sources[ch] != "" {
			requested[ch] = strings.TrimSpace(body.Sources[ch])
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].AuthIndex < files[j].AuthIndex })
	credentials := map[string]hubCredential{}
	sources := make([]hubSource, len(hubChannels))
	failures := []map[string]string{}
	for i, ch := range hubChannels {
		sources[i] = hubSource{Channel: ch, Status: "no_account", Accounts: []hubAccount{}, Endpoints: []directorySource{}}
	}
	active := getActiveAuthID()
	for _, file := range files {
		if ctx.Err() != nil {
			return map[string]any{"error": "source lookup canceled or timed out"}
		}
		sa, _, err := hostAuthGetBundle(file.AuthIndex)
		if err != nil || sa == nil {
			failures = append(failures, map[string]string{"auth_index": file.AuthIndex, "name": file.Name, "error": "credential lookup failed"})
			continue
		}
		raw, err := storedAuthJSON(sa)
		if err != nil {
			continue
		}
		token, _ := extractAccessToken(raw)
		channel := serviceRealmForStorage(raw, token)
		available := !file.Disabled && token != ""
		name := sa.Account.Nickname
		if name == "" {
			name = file.Name
		}
		credentials[file.AuthIndex] = hubCredential{File: file, Auth: sa, Channel: channel, Available: available}
		for i := range sources {
			if sources[i].Channel == channel {
				sources[i].Accounts = append(sources[i].Accounts, hubAccount{ID: file.AuthIndex, Name: name, Selected: active != "" && active == file.ID, Available: available})
			}
		}
	}
	grouped := make([][]hubVariant, len(sources))
	var wg sync.WaitGroup
	for i := range sources {
		a, basis := chooseHubAccount(sources[i].Accounts, requested[sources[i].Channel])
		sources[i].Basis = basis
		if a.ID == "" {
			sources[i].Status = basis
			continue
		}
		sources[i].Account = a.ID
		sources[i].Name = a.Name
		credential := credentials[a.ID]
		wg.Add(1)
		go func(index int, credential hubCredential) {
			defer wg.Done()
			result := resolveAccountDirectory(ctx, credential.Auth, force)
			s := &sources[index]
			s.Status = result.Status
			s.Count = len(result.Models)
			s.FetchedAt = result.FetchedAt
			s.Cached = result.Cached
			s.Warning = result.Warning
			s.Endpoints = result.Sources
			for _, m := range result.Models {
				grouped[index] = append(grouped[index], hubVariant{Origin: "dynamic", Channel: s.Channel, Account: s.Account, Model: m})
			}
		}(i, credential)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return map[string]any{"error": "model hub canceled or timed out"}
	}
	variants := []hubVariant{}
	partial := len(failures) > 0
	for i, s := range sources {
		variants = append(variants, grouped[i]...)
		if s.Status != "ok" && s.Status != "no_account" {
			partial = true
		}
	}
	variants = append(variants, customHubVariants()...)
	rows := mergeHubModels(variants)
	status := "ok"
	if partial {
		status = "partial"
	}
	return map[string]any{"status": status, "models": rows, "sources": sources, "account_errors": failures, "count": len(rows)}
}
