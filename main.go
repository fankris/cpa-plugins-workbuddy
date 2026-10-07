// Package main implements the workbuddy CLIProxyAPI dynamic plugin.
//
// workbuddy wraps Tencent CodeBuddy (copilot.tencent.com) as a cliproxy
// provider: it performs the CodeBuddy web login flow, refreshes access
// tokens, and forwards OpenAI-compatible chat completion requests to the
// upstream /v2/chat/completions endpoint.
//
// This file is a clean-room reimplementation reconstructed from the public
// workbuddy.so binary (symbol table, string constants and RPC shape) published
// by Sliverkiss. Original credit for the workbuddy plugin goes to Sliverkiss;
// see https://github.com/Sliverkiss/cpa-plugin. Built with -buildmode=c-shared
// and exports the cliproxy C ABI entry points.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
        void* ptr;
        size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
        uint32_t abi_version;
        void* host_ctx;
        cliproxy_host_call_fn call;
        cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
        uint32_t abi_version;
        cliproxy_plugin_call_fn call;
        cliproxy_plugin_free_fn free_buffer;
        cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

// Wrappers so Go can invoke the host function-pointer table via cgo. The host
// API captured at init is used to push streaming chunks back asynchronously.
static int wb_call_host(const cliproxy_host_api* api, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
        return api->call(api->host_ctx, method, request, request_len, response);
}
static void wb_free_host_buffer(cliproxy_host_api* api, void* ptr, size_t len) {
        api->free_buffer(ptr, len);
}

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	providerName  = "workbuddy"
	authFileName  = "workbuddy.json"
	pluginLogoURL = "https://raw.githubusercontent.com/DGZSbot/ai-icon/refs/heads/main/WorkBuddy.png"
	// CN chat/auth gateway (iss = codebuddy.cn realm).
	upstreamBaseCN = "https://copilot.tencent.com"
	// Global chat/auth gateway (iss = workbuddy.ai realm). APISIX on
	// copilot.tencent.com rejects Global JWTs with 401; must use workbuddy.ai.
	upstreamBaseGlobal = "https://www.workbuddy.ai"
	// Legacy CB login entrypoint. WB/CB share the foreign account channel;
	// existing-account business requests default to the WB gateway.
	// Retained for the unchanged native login integration.
	upstreamBaseIntl    = "https://www.codebuddy.ai"
	clientUA            = "CLI/2.108.1 CodeBuddy/2.108.1"
	clientUAWorkBuddy   = "CLI/2.108.1 WorkBuddy/2.108.1"
	originReferer       = "https://www.codebuddy.cn"
	originRefererGlobal = "https://www.workbuddy.ai"
	originRefererIntl   = "https://www.codebuddy.ai"

	// CN endpoint aliases (login / chat / models). upstreamBaseCN is the only
	// CN base; Global has its own upstreamBaseGlobal. No "upstreamBase" legacy
	// alias — removed in v0.6.31 dead-code sweep.
	endpointAuthStateBase = upstreamBaseCN + "/v2/plugin/auth/state?platform="
	endpointLoginAcct     = upstreamBaseCN + "/v2/plugin/login/account?state="
	endpointAuthToken     = upstreamBaseCN + "/v2/plugin/auth/token?state="
	endpointTokenRefresh  = upstreamBaseCN + "/v2/plugin/auth/token/refresh"
	endpointChat          = upstreamBaseCN + "/v2/chat/completions"
	endpointModels        = upstreamBaseCN + "/console/enterprises/personal/models"

	loginTTL = 5 * time.Minute
)

// loginCtx holds the cookie-affined host HTTP session for one in-flight login
// flow. CodeBuddy associates the browser login with the state issued at
// auth/state, so the same isolated cookie set is reused across polls.
type loginCtx struct {
	cookies  *loginCookieState
	region   string // login realm: cn | intl (merged codebuddy-intl)
	platform string // client platform used to mint the token: CLI | ide
	expires  time.Time
}

var (
	hostAPI          *C.cliproxy_host_api // captured at init, used for async host calls
	hostAPIMu        sync.RWMutex
	loginStates      sync.Map // state(string) -> *loginCtx
	httpClientOnce   sync.Once
	sharedClient     *http.Client
	loginJanitorOnce sync.Once
)

// loginStatesPruneInterval bounds how often the janitor sweeps abandoned
// login states (user started a login but never finished).
const loginStatesPruneInterval = time.Minute

func startLoginJanitor() {
	loginJanitorOnce.Do(func() {
		if !pluginWorkerStart() {
			return
		}
		go func() {
			defer pluginWorkerDone()
			ticker := time.NewTicker(loginStatesPruneInterval)
			defer ticker.Stop()
			for {
				select {
				case <-pluginContext().Done():
					return
				case <-ticker.C:
					now := time.Now()
					loginStates.Range(func(key, value any) bool {
						if lc, ok := value.(*loginCtx); ok && now.After(lc.expires) {
							loginStates.Delete(key)
						}
						return true
					})
				}
			}
		}()
	})
}

func init() { startLoginJanitor() }

func main() {}

// -----------------------------------------------------------------------------
// C ABI exports
// -----------------------------------------------------------------------------

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	hostAPIMu.Lock()
	hostAPI = host
	hostAPIMu.Unlock()
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	// Drain while the host callback table is still valid. Never clear hostAPI
	// before workers finish: async stream cleanup still calls host.stream.close.
	shutdownPlugin()
}

// -----------------------------------------------------------------------------
// Host calls (async streaming + auth callbacks)
// -----------------------------------------------------------------------------

// hostRPCTestOverride, when non-nil, answers hostCall instead of the real host
// function-pointer table. Production leaves it nil. Tests install it to drive
// the host.auth.* store (list/get/save) without a live CPA process, which is
// what makes the lifecycle state machine testable.
var hostRPCTestOverride func(method string, request []byte) ([]byte, error)

// hostCall invokes a host RPC method via the function-pointer table captured
// at init. Used to push stream chunks back asynchronously (host.stream.emit /
// host.stream.close) and to read the host's auth store (host.auth.list/get).
func hostCall(method string, request []byte) ([]byte, error) {
	if override := hostRPCTestOverride; override != nil {
		return override(method, request)
	}
	allowCleanup := method == pluginabi.MethodHostStreamClose || method == pluginabi.MethodHostHTTPStreamClose || method == pluginabi.MethodHostHTTPCancel
	if !hostCallbackStart(allowCleanup) {
		return nil, errPluginQuiescing
	}
	defer hostCallbackDone()
	hostAPIMu.RLock()
	api := hostAPI
	if api == nil || api.call == nil {
		hostAPIMu.RUnlock()
		return nil, fmt.Errorf("host API unavailable")
	}
	hostAPIMu.RUnlock()
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var cReq unsafe.Pointer
	var reqLen C.size_t
	if len(request) > 0 {
		cReq = C.CBytes(request)
		defer C.free(cReq)
		reqLen = C.size_t(len(request))
	}
	var resp C.cliproxy_buffer
	rc := C.wb_call_host(api, cMethod, (*C.uint8_t)(cReq), reqLen, &resp)
	var out []byte
	if resp.ptr != nil && resp.len > 0 {
		out = C.GoBytes(resp.ptr, C.int(resp.len))
	}
	if resp.ptr != nil && api.free_buffer != nil {
		C.wb_free_host_buffer(api, resp.ptr, resp.len)
	}
	if rc != 0 {
		return out, fmt.Errorf("host call %s returned %d", method, int(rc))
	}
	return out, nil
}

// -----------------------------------------------------------------------------

func handleMethod(method string, request []byte) ([]byte, error) {
	if pluginQuiescing() && method != pluginabi.MethodPluginQuiesce &&
		method != pluginabi.MethodPluginRegister && method != pluginabi.MethodPluginReconfigure {
		return errorEnvelope("quiescing", errPluginQuiescing.Error()), nil
	}
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := validateLifecycleConfig(request); err != nil {
			return errorEnvelope("invalid_config", err.Error()), nil
		}
		configure(request)
		// Loud one-liner so deployments can verify WHICH build is actually
		// running: grep this in the CPA log after every plugin update.
		// Only allowlisted field names survive the host's log formatter
		// (logFieldOrder), so schema_version is inlined into the message
		// instead of being sent as a field that would be dropped.
		hostLog(logLevelInfo, fmt.Sprintf("plugin %s registered (pluginabi schema %d)", version, pluginabi.SchemaVersion), map[string]any{
			"version":  version,
			"provider": providerName,
		})
		return okEnvelope(wbRegistration())
	case pluginabi.MethodPluginQuiesce:
		quiescePlugin()
		return okEnvelope(map[string]any{})
	case pluginabi.MethodModelStatic:
		return handleModelStatic(request)
	case pluginabi.MethodModelForAuth:
		return handleModelForAuth(request)
	case pluginabi.MethodAuthIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerName})
	case pluginabi.MethodAuthParse:
		return handleParseAuth(request)
	case pluginabi.MethodAuthLoginStart:
		return handleStartLogin(request)
	case pluginabi.MethodAuthLoginPoll:
		return handlePollLogin(request)
	case pluginabi.MethodAuthRefresh:
		return handleRefreshAuth(request)
	case pluginabi.MethodQuotaIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerName})
	case pluginabi.MethodQuotaDescribe:
		return handleQuotaDescribe(request)
	case pluginabi.MethodQuotaFetch:
		return handleQuotaFetch(request)
	case pluginabi.MethodQuotaReset:
		return handleQuotaReset(request)
	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerName})
	case pluginabi.MethodExecutorExecute:
		return handleExecExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecStream(request)
	case pluginabi.MethodExecutorCountTokens:
		// Upstream CodeBuddy has no dedicated count_tokens API, so this is a
		// local estimate. Returning a hardcoded 0 (the old behaviour) told
		// Claude Code and other context-budgeting clients that the prompt was
		// free: they never compressed, and the real request then blew past the
		// upstream limit. Estimate from the payload instead, deliberately
		// biased HIGH — an over-estimate makes the client compact a little
		// early, an under-estimate makes the request fail outright.
		return okEnvelope(pluginapi.ExecutorResponse{Payload: estimateInputTokensPayload(request)})
	case pluginabi.MethodManagementRegister:
		// Cache host-injected BasePath so handleManagement doesn't hardcode
		// /v0/management (v0.6.31: tolerate future host path changes).
		var regReq pluginapi.ManagementRegistrationRequest
		if err := json.Unmarshal(request, &regReq); err == nil {
			if regReq.BasePath != "" {
				setManagementBasePath(regReq.BasePath)
			}
			if regReq.ResourceBasePath != "" {
				setResourceBasePath(regReq.ResourceBasePath)
			}
		}
		return okEnvelope(managementRegistration())
	case pluginabi.MethodManagementHandle:
		return handleManagement(request)
	case pluginabi.MethodSchedulerPick:
		return handleSchedulerPick(request)
	case pluginabi.MethodUsageHandle:
		return handleUsageRecord(request)
	case pluginabi.MethodRequestComplete:
		return handleRequestComplete(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

// -----------------------------------------------------------------------------
// Registration & models
// -----------------------------------------------------------------------------

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code string `json:"code"`
	// Message is the human-readable failure description.
	Message string `json:"message"`
	// HTTPStatus follows the plugin development spec: executor failures must
	// report the upstream status (401/403/404/429/5xx) so CPA classifies the
	// error for the client and core cooldown handling. Omitted (0) defaults
	// to a 500 that clients treat as a transient gateway outage.
	HTTPStatus int `json:"http_status,omitempty"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type streamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

type registrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	FrontendAuthProvider  bool                         `json:"frontend_auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
	Scheduler             bool                         `json:"scheduler"`
	ManagementAPI         bool                         `json:"management_api"`
	QuotaProvider         bool                         `json:"quota_provider"`
	// UsagePlugin receives one UsageRecord per completed request. WorkBuddy
	// uses it ONLY to accumulate the per-model daily free-token counters shown
	// in the panel: upstream exposes no per-model quota endpoint, so the
	// "daily free allowance" figure has to be measured locally. This is
	// separate from QuotaProvider, which reports PAID credits (account-level,
	// package cycles) fetched from the billing API.
	UsagePlugin bool `json:"usage_plugin"`
	// RequestLifecyclePlugin receives one terminal event per request that
	// reached request interception, including requests that never reach this
	// plugin's executor (no usable credential). UsagePlugin cannot see those,
	// so "traffic arrived and none of it was served" is invisible without this.
	//
	// Measured boundary (CPA v7.3.12): the tracker is created AFTER provider
	// resolution, so a request failing on an unknown MODEL yields no event;
	// a request that resolves to workbuddy but finds no usable credential does.
	RequestLifecyclePlugin bool `json:"request_lifecycle_plugin"`
}

// version is injected at build time via -ldflags "-X main.version=...".
var version = "v8.0.15-1.0.59"

func wbRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "WorkBuddy",
			Version:          version,
			Author:           "Aiseek",
			GitHubRepository: "https://github.com/fankris/cpa-plugins-workbuddy",
			Logo:             pluginLogoURL,
			// Native metadata is global, not per-browser locale. CPAMC owns
			// touched-field saves; complex settings remain in its YAML editor.
			ConfigFields: []pluginapi.ConfigField{
				{Name: "login_region", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"cn", "intl"}, Description: "首次登录/添加账号的授权渠道：CN（cn，默认）或 Intl（intl）。WorkBuddy / CodeBuddy 为入口名称。只影响新登录，已有账号不变。"},
				{Name: "login_platform", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"CLI", "ide"}, Description: "首次登录的客户端类型。保持 CLI（默认，对应 WorkBuddy 应用）；需要 CodeBuddy IDE 登录时选 ide。只影响新登录。"},
				{Name: "scheduler_mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"host", "builtin", "off", "credits"}, Description: "host：跟随 CPA 当前调度策略；builtin：兼容模式，显式 round-robin；off：builtin 的历史别名，不是关闭请求；credits：插件选择面板活动账号，不是按余额排序。默认 host，旧配置的显式模式保留；切换即时生效，不改写宿主策略。"},
			},
		},
		Capabilities: registrationCapability{
			ModelProvider:          true,
			AuthProvider:           true,
			FrontendAuthProvider:   false,
			Executor:               true,
			ExecutorModelScope:     pluginapi.ExecutorModelScopeOAuth,
			ExecutorInputFormats:   []string{"chat-completions"},
			ExecutorOutputFormats:  []string{"chat-completions"},
			ManagementAPI:          true,
			Scheduler:              true,
			QuotaProvider:          true,
			UsagePlugin:            true,
			RequestLifecyclePlugin: true,
		},
	}
}

// dynamicModelsCacheTTL bounds how long a fetched model list is reused.
// model.static / model.for_auth are re-invoked by CPA on every config reload
// and on each models query; without caching, every reload fans out to one
// upstream call per account.
const dynamicModelsCacheTTL = 5 * time.Minute

// realmModelsEntry is one realm's cached discovery result plus the v0.9.9
// diagnostics trail: WHERE the advertised list came from (discovery / pin /
// static fallback), when it was fetched, and why discovery failed last time.
// v0.12.18: the cache is keyed by realm (cn|global|intl) — a single shared
// entry let one realm's answer (or CN-flavored static fallback) satisfy
// model.for_auth for accounts on another realm, advertising models their
// gateway never served. Error-only entries (models nil) are cache misses for
// fetch purposes but keep the last failure visible to the panel.
type realmModelsEntry struct {
	details   map[string]modelDetails
	models    []pluginapi.ModelInfo
	fetched   time.Time
	source    string // "discovery" | "pin ..." | "static ..."
	srcCount  int    // display count for pin/static states (models stays nil)
	lastErr   string
	lastErrAt time.Time
	lastLogAt time.Time // throttles the discovery-failure log line
}

var dynamicModelsCache = struct {
	sync.RWMutex
	realms map[string]realmModelsEntry
}{realms: map[string]realmModelsEntry{}}

//
// CPA applies oauth-model-alias to the models this plugin registers, so the
// gateway may route a request whose model ID is an alias (e.g.
// "point/deepseek-v4-flash") to this executor. The upstream only knows the
// real model IDs, so the plugin must map the alias back before forwarding.
//
// ExecutorRequest carries no host config, so the alias table is cached from
// the AuthModelRequest.Host summary every time the host asks for models
// (model.static / model.for_auth are re-queried by CPA on config reload,
// keeping this cache in sync with oauth-model-alias changes). Auth-level
// attribute overrides ("model_alias"/"model-alias"/"oauth-model-alias")
// are parsed per request and take precedence over the global table.

var modelAliasCache struct {
	sync.RWMutex
	byAlias map[string]string
}

// ------------------------------------------------------------------------------
// Usage reporting (request monitoring)
// ------------------------------------------------------------------------------
//
// The host reports it. CPA ≥ v7.2.146 wraps plugin executors in the core
// usage reporter, so every request lands in the host usage queue that CPAMP
// pulls; usage.handle (usage.go) is a no-op acknowledgement. The plugin used
// to also POST NDJSON to CPAMP /v0/management/usage/import, which duplicated
// every request — removed in 0.9.30.

// storedAuth is the on-disk shape of a workbuddy credential.
type storedAuth struct {
	Auth    storedTokens  `json:"auth"`
	Account storedAccount `json:"account"`
}

type storedTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"`
	Domain       string `json:"domain"`
	// LoginPlatform records which client produced the token: "CLI"
	// (workbuddy login) or "ide" (CodeBuddy IDE login). Empty means legacy
	// CLI-style (no X-IDE-* headers), matching historical workbuddy files.
	LoginPlatform string `json:"loginPlatform,omitempty"`
	// Region records which realm produced the token: "cn" (copilot.tencent.com),
	// "intl" (codebuddy.ai IDE client) or "global" (workbuddy.ai panel import).
	// Empty = legacy file — accountRegion falls back to domain sniffing.
	Region string `json:"region,omitempty"`
}

type storedAccount struct {
	UID          string `json:"uid"`
	EnterpriseID string `json:"enterpriseId"`
	Nickname     string `json:"nickname"`
}

// apiEnvelope is the generic {code,msg,data} wrapper used by every CodeBuddy API.
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type tokenData struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresIn        int64  `json:"expiresIn"`
	RefreshExpiresIn int64  `json:"refreshExpiresIn"`
	Domain           string `json:"domain"`
}

type accountData struct {
	UID          string `json:"uid"`
	EnterpriseID string `json:"enterpriseId"`
	Nickname     string `json:"nickname"`
}

type authStateData struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}

func parseStored(raw []byte) (*storedAuth, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty auth storage")
	}
	// Accept both shapes seen in the wild:
	//   nested: {"auth":{"accessToken":...},"account":{"uid":...}} (plugin/oauth output)
	//   flat:   {"accessToken":...,"uid":...,"nickname":...} (CPA-Manager-Plus auths/workbuddy.json)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("storage_parse_error: %w", err)
	}
	var sa storedAuth
	if _, nested := probe["auth"]; nested {
		if err := json.Unmarshal(raw, &sa); err != nil {
			return nil, fmt.Errorf("storage_parse_error: %w", err)
		}
	} else {
		var flat struct {
			AccessToken   string `json:"accessToken"`
			RefreshToken  string `json:"refreshToken"`
			ExpiresAt     int64  `json:"expiresAt"`
			Domain        string `json:"domain"`
			Region        string `json:"region"`
			LoginPlatform string `json:"loginPlatform"`
			UID           string `json:"uid"`
			EnterpriseID  string `json:"enterpriseId"`
			Nickname      string `json:"nickname"`
			// Identity fallbacks written by this plugin's own save path
			// (buildAuthFileJSON) and by CPA management edits. Without these
			// the account name is dropped on every re-read: the file carries
			// "email"/"account_name" but the parser only looked at "nickname",
			// so a saved account rendered as the bare provider name.
			Email       string `json:"email"`
			AccountName string `json:"account_name"`
		}
		if err := json.Unmarshal(raw, &flat); err != nil {
			return nil, fmt.Errorf("storage_parse_error: %w", err)
		}
		sa.Auth = storedTokens{AccessToken: flat.AccessToken, RefreshToken: flat.RefreshToken, ExpiresAt: flat.ExpiresAt, Domain: flat.Domain, Region: flat.Region, LoginPlatform: flat.LoginPlatform}
		sa.Account = storedAccount{UID: flat.UID, EnterpriseID: flat.EnterpriseID, Nickname: flat.Nickname}
		if strings.TrimSpace(sa.Account.Nickname) == "" {
			sa.Account.Nickname = firstNonEmptyTrimmed(flat.AccountName, flat.Email)
		}
	}
	// Manager stores the service as auth.realm; retain it before re-serialization.
	// This is a read compatibility mapping, not an auth-store write or migration.
	var routing struct {
		Realm  string `json:"realm"`
		Region string `json:"region"`
		Domain string `json:"domain"`
		Auth   struct {
			Realm string `json:"realm"`
		} `json:"auth"`
	}
	if json.Unmarshal(raw, &routing) == nil {
		if sa.Auth.Region == "" {
			sa.Auth.Region = firstNonEmptyTrimmed(routing.Auth.Realm, routing.Region, routing.Realm)
		}
		if sa.Auth.Domain == "" {
			sa.Auth.Domain = strings.TrimSpace(routing.Domain)
		}
	}
	// Top-level identity also applies to the nested shape: the plugin writes
	// email/account_name at the top level (that is where the host reads Label
	// from), so a nested credential re-read must pick them up too. An explicit
	// nickname inside "account" always wins.
	if strings.TrimSpace(sa.Account.Nickname) == "" {
		var top struct {
			Email       string `json:"email"`
			AccountName string `json:"account_name"`
		}
		if json.Unmarshal(raw, &top) == nil {
			sa.Account.Nickname = firstNonEmptyTrimmed(top.AccountName, top.Email)
		}
	}
	if sa.Auth.AccessToken == "" {
		return nil, fmt.Errorf("parse_error: missing accessToken")
	}
	return &sa, nil
}

// firstNonEmptyTrimmed returns the first non-blank value after trimming, or "".
func firstNonEmptyTrimmed(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// -------------------------------------------------------------------------------
// HTTP plumbing
// -------------------------------------------------------------------------------

// commonHeaders keeps the legacy CN defaults used by login helpers that do not
// have a stored credential yet. Existing-account requests should use one of the
// realm-aware helpers below so Origin, Referer, and User-Agent stay aligned with
// the credential that will be sent upstream.
func commonHeaders(req *http.Request) {
	commonHeadersForRealm(req, regionCN)
}

func commonHeadersForRealm(req *http.Request, realm string) {
	if req == nil {
		return
	}
	origin := originRefererForRealm(realm)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", userAgentForRealm(realm))
}

func commonHeadersForAuth(req *http.Request, sa *storedAuth) {
	realm := regionCN
	if sa != nil {
		realm = accountServiceRegion(sa)
	}
	commonHeadersForRealm(req, realm)
	if sa != nil {
		req.Header.Set("User-Agent", userAgentForAuth(sa))
	}
}

func originRefererForRealm(realm string) string {
	switch strings.ToLower(strings.TrimSpace(realm)) {
	case regionGlobal:
		return originRefererGlobal
	case regionIntl:
		return originRefererIntl
	default:
		return originReferer
	}
}

// userAgentForRealm mirrors the client identity expected by each upstream
// realm. Global is the WorkBuddy desktop realm; CN and Intl use CodeBuddy.
func userAgentForRealm(realm string) string {
	if strings.EqualFold(strings.TrimSpace(realm), regionGlobal) {
		return clientUAWorkBuddy
	}
	return clientUA
}

func userAgentForAuth(sa *storedAuth) string {
	if sa == nil {
		return clientUA
	}
	if isWorkBuddyService(sa) {
		return clientUAWorkBuddy
	}
	return clientUA
}

// authHeadersFor builds the shared identity headers for existing credentials.
// Refresh is the only mode that receives X-Refresh-Token; chat, billing, and
// model discovery never send that long-lived credential upstream.
func authHeadersFor(req *http.Request, sa *storedAuth, refresh bool) {
	if req == nil || sa == nil {
		return
	}
	commonHeadersForAuth(req, sa)
	applyPlatformHeaders(req, platformForAuth(sa))
	applyRealmHeaders(req, sa)
	// Stable per-account device identity (anti-abuse): one virtual device per
	// account, stable across requests and restarts. See fingerprint.go.
	applyFingerprintHeaders(sa.Account.UID, req.Header.Set)
	if sa.Auth.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	} else {
		req.Header.Set("X-No-Authorization", "true")
	}
	if sa.Account.UID != "" {
		req.Header.Set("X-User-Id", sa.Account.UID)
	} else {
		req.Header.Set("X-No-User-Id", "true")
	}
	if sa.Account.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", sa.Account.EnterpriseID)
		req.Header.Set("X-Tenant-Id", sa.Account.EnterpriseID)
	} else {
		req.Header.Set("X-No-Enterprise-Id", "true")
		req.Header.Set("X-No-Tenant-Id", "true")
	}
	if domain := normalizeAuthDomain(sa.Auth.Domain); domain != "" {
		req.Header.Set("X-Domain", domain)
	} else {
		req.Header.Set("X-No-Department-Info", "true")
	}
	req.Header.Set("X-Product", "SaaS")
	if refresh {
		if token := strings.TrimSpace(sa.Auth.RefreshToken); token != "" {
			req.Header.Set("X-Refresh-Token", token)
		}
		req.Header.Set("X-Auth-Refresh-Source", "plugin")
	} else {
		req.Header.Del("X-Refresh-Token")
		req.Header.Del("X-Auth-Refresh-Source")
	}
}

// originRefererFor returns the Origin/Referer base URL appropriate for the
// account's detected realm.
func originRefererFor(sa *storedAuth) string {
	if sa != nil {
		return originRefererForRealm(accountServiceRegion(sa))
	}
	return originReferer
}

// upstreamBaseFor returns the chat/auth API host for the account realm.
// Mixing realms yields APISIX 401, so every existing-account request must use
// this function rather than a package-level CN default.
func upstreamBaseFor(sa *storedAuth) string {
	if sa != nil {
		return upstreamBaseForAuth(sa)
	}
	return upstreamBaseCN
}

func endpointChatFor(sa *storedAuth) string {
	return upstreamBaseFor(sa) + "/v2/chat/completions"
}

func endpointTokenRefreshFor(sa *storedAuth) string {
	return upstreamBaseFor(sa) + "/v2/plugin/auth/token/refresh"
}

func endpointModelsFor(sa *storedAuth) string {
	return upstreamBaseFor(sa) + "/console/enterprises/personal/models"
}

// backendHeaders applies auth-derived headers to a chat completion request.
// Empty fields are signalled via the X-No-* convention used by CodeBuddy.
func backendHeaders(req *http.Request, sa *storedAuth) {
	authHeadersFor(req, sa, false)
}

// -----------------------------------------------------------------------------
// Auth handlers
// -----------------------------------------------------------------------------

// isOurFamilyFileName reports whether a type-less auth file belongs to this
// plugin by name: our canonical prefix, a pre-merge family prefix, or a
// legacy single-file name. The filename is the only trustworthy discriminator
// for files without an explicit type (see the ownership note in
// handleParseAuth).
func isOurFamilyFileName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, p := range []string{providerName + "-", "codebuddy-cn-", "codebuddy-intl-"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	switch lower {
	case "workbuddy.json", "codebuddy.json", "codebuddy-cn.json", "codebuddy-intl.json":
		return true
	}
	return false
}

// isOurDeclaredType reports whether an explicitly declared auth "type"
// belongs to this plugin's family (current name plus pre-merge names).
func isOurDeclaredType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "workbuddy", "workbuddy-cn", "workbuddy-global", "workbuddy-intl",
		"codebuddy", "codebuddy-cn", "codebuddy-intl":
		return true
	}
	return false
}

func handleParseAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// Ownership check (CPA native contract): the host routes by the file's
	// top-level "type" field (synthesizer/file.go). Files without a type fall
	// back to polling every plugin — first Handled=true wins. Only claim files
	// whose declared type matches us — or, for type-less legacy files, when the
	// host already routed this to us or the filename carries our prefix.
	// Symmetric with the qoderwork plugin's guard (commit 7b776a9).
	var probeType struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(req.RawJSON, &probeType)
	declared := strings.ToLower(strings.TrimSpace(probeType.Type))
	if declared != "" && !isOurDeclaredType(declared) {
		// Explicitly another provider's file — never claim it. Family-wide:
		// pre-merge names (codebuddy/codebuddy-cn/codebuddy-intl) remain ours.
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}
	if declared == "" {
		// No type declared: claim ONLY when the filename carries our family.
		// NOTE: req.Provider cannot prove ownership here — the host's
		// callParseAuths rewrites an empty Provider to the POLLED plugin's own
		// identifier, so EqualFold(req.Provider, "workbuddy") is always true
		// while polling us regardless of the file's origin. Symmetric with the
		// qoder plugin's fix (repo v0.12.9).
		if !isOurFamilyFileName(req.FileName) {
			return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
		}
	}
	sa, err := parseStored(req.RawJSON)
	if err != nil {
		// Not a workbuddy credential; let the host try other providers.
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}
	// CRITICAL: echo back the host-provided FileName AND leave ID empty.
	//
	// CPA uses ID for auth record identity (upsert key). If we set ID=uid
	// while the host's watcher initially registered with ID=filename,
	// upsertAuthRecord can't find the existing record → creates a NEW one
	// → duplicate auth entries (same file, different IDs).
	//
	// By leaving ID empty, CPA falls back to authIDForPath(path) which
	// derives ID from the file path → always matches the watcher's key.
	// FileName is also echoed back to avoid rename-based duplicates.
	ad := toAuthDataOpts(sa, nil, false)
	ad.ID = "" // let host compute from path (prevents ID mismatch dupes)
	if fn := strings.TrimSpace(req.FileName); fn != "" {
		ad.FileName = fn
	}
	return okEnvelope(pluginapi.AuthParseResponse{
		Handled: true,
		Auth:    ad,
	})
}

func toAuthData(sa *storedAuth) pluginapi.AuthData {
	return toAuthDataOpts(sa, nil, false)
}

// toAuthDataOpts builds AuthData with optional credits snapshot and disabled flag.
func toAuthDataOpts(sa *storedAuth, cr *creditsSummary, disabled bool) pluginapi.AuthData {
	storage, _ := json.Marshal(sa)
	id := providerName
	fileName := authFileName
	if sa != nil {
		if uid := sanitizeUIDForFileName(sa.Account.UID); uid != "" {
			id = uid
			fileName = authFileNameFor(sa)
		}
	}
	label := labelForAuth(sa)
	meta := enrichAuthMetadata(sa, cr, disabled)
	return pluginapi.AuthData{
		Provider:    providerName,
		ID:          id,
		FileName:    fileName,
		Label:       label,
		Disabled:    disabled,
		StorageJSON: storage,
		// Standardized auth metadata. `type` is required by the host for
		// auth-file classification; `logo`/`note`/`disabled` surface on auth rows.
		Metadata: meta,
	}
}

// -----------------------------------------------------------------------------

func handleExecExecute(raw []byte) ([]byte, error) {
	var req executorStreamRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := parseStored(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	// Resolve oauth-model-alias (e.g. "point/deepseek-v4-flash") back to the
	// real upstream model ID; the upstream rejects unknown alias IDs.
	upstreamModel := resolveUpstreamModel(req.Model, req.AuthAttributes)
	authUID := ""
	if sa.Account.UID != "" {
		authUID = sa.Account.UID
	}
	// CodeBuddy rejects non-stream requests (code 11101), so always stream
	// upstream and fold the chunks into a single chat.completion object.
	// prepareUpstreamBody does forceStream + normalizeTools + rewriteSystem +
	// ensureSystemMessage + rewriteModel in ONE unmarshal/marshal pass.
	body := prepareUpstreamBody(req.Payload, req.OriginalRequest, sa, upstreamModel)
	httpReq, err := http.NewRequestWithContext(withHostCallbackID(pluginContext(), req.HostCallbackID), http.MethodPost, endpointChatFor(sa), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	backendHeaders(httpReq, sa)
	// Compliance: route via host.http.do_stream so request-log captures the
	// outbound call. Read entire body via the bridge, then fold SSE → completion.
	stream, statusCode, _, err := openChatStreamWithReasoningRetry(httpReq, body)
	if err != nil {
		return errorEnvelope("upstream_error", fmt.Sprintf("http_error: %v", err), http.StatusBadGateway), nil
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if statusCode >= 400 {
		payload, _ := io.ReadAll(reader)
		reconcileAfterExecutorError(req.AuthID, statusCode, string(payload))
		// v0.12.18: 11102 model-catalog rejections become a bilingual,
		// realm-aware error; v1.0.31 adds 6004 model-throttle naming and
		// remaps content-review 403s to request-scoped 400 (CPA would
		// otherwise cool the credential for blocked content). Other failures
		// keep the raw shape, and the effective status rides the envelope so
		// CPA maps 401/403/429/5xx for the client instead of a blanket 500.
		errStatus, errChat := translateChatUpstreamError(statusCode, string(payload), sa)
		return errorEnvelope("upstream_error", errChat.Error(), errStatus), nil
	}
	completion, err := aggregateCompletion(reader, req.Model)
	if err != nil {
		// An empty-but-well-formed upstream answer is a rate limit (429), so CPA
		// cools this credential and retries the next account; other aggregation
		// failures stay 502 (malformed stream).
		return errorEnvelope("upstream_error", err.Error(), chatErrorStatus(err, http.StatusBadGateway)), nil
	}
	invalidateAccountCredits(req.AuthID, authUID)
	return okEnvelope(pluginapi.ExecutorResponse{Payload: completion})
}

// executorStreamRequest wraps the host's executor.execute_stream RPC: the
// ExecutorRequest plus the async stream id the host uses to receive chunks.
type executorStreamRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleExecStream(raw []byte) ([]byte, error) {
	var req executorStreamRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := parseStored(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	upstreamModel := resolveUpstreamModel(req.Model, req.AuthAttributes)
	authUID := ""
	if sa.Account.UID != "" {
		authUID = sa.Account.UID
	}
	body := req.Payload
	if len(body) == 0 {
		body = req.OriginalRequest
	}
	// Single-pass JSON rewrite (see handleExecExecute for the non-stream path).
	body = prepareUpstreamBody(body, nil, sa, upstreamModel)

	headers := streamHeaders()
	sseFramed := clientNeedsSSEFrame(req.Metadata)

	// No async stream id → fall back to synchronous chunk collection.
	if req.StreamID == "" {
		chunks, statusCode, errCollect := collectUpstreamStream(body, sa, sseFramed, withHostCallbackID(pluginContext(), req.HostCallbackID))
		if errCollect != nil {
			// Spec: executor.execute_stream errors carry the upstream status
			// too (statusCode < 400 means a malformed stream, not an HTTP
			// rejection → 502 bad gateway). An empty-but-well-formed answer is
			// the exception: it maps to 429 so CPA cools the credential.
			status := statusCode
			if status < 400 {
				status = http.StatusBadGateway
			}
			status = chatErrorStatus(errCollect, status)
			return errorEnvelope("upstream_error", errCollect.Error(), status), nil
		}
		invalidateAccountCredits(req.AuthID, authUID)
		return okEnvelope(streamResponse{Headers: headers, Chunks: chunks})
	}

	// Async: return immediately with empty chunks. A goroutine pumps the upstream
	// and emits each chunk via host.stream.emit so the client sees true streaming.
	// Use context.Background() (not nil) so the request can be cancelled when the
	// client disconnects — otherwise the pump keeps reading a dead upstream until
	// sharedHTTPClient's 120s timeout, holding a pool slot the whole time.
	ctx, cancel := context.WithCancel(withHostCallbackID(pluginContext(), req.HostCallbackID))
	registered, ok := registerAsyncStream(req.StreamID, cancel)
	if !ok {
		cancel()
		return nil, errPluginQuiescing
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointChatFor(sa), bytes.NewReader(body))
	if err != nil {
		registered.finish()
		cancel()
		streamEmitError(req.StreamID, err.Error())
		registered.closeHostStream()
		return okEnvelope(streamResponse{Headers: headers})
	}
	backendHeaders(httpReq, sa)
	go pumpUpstreamStream(httpReq, registered, req.StreamID, sseFramed, authUID, req.AuthID, sa, body)
	return okEnvelope(streamResponse{Headers: headers})

}

// -----------------------------------------------------------------------------

func okEnvelope(v any) ([]byte, error) {
	result, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func errorEnvelope(code, message string, httpStatus ...int) []byte {
	status := 0
	if len(httpStatus) > 0 {
		status = httpStatus[0]
	}
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: status}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
