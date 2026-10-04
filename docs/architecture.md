# WorkBuddy Plugin Architecture

Module map and data flow for the workbuddy plugin. The plugin is a single
`package main` compiled as a c-shared `.so`, loaded by CPA at startup and
driven via the `pluginabi` RPC interface. Its public regions are CN and Intl; `workbuddy.ai` remains a distinct internal service identity so legacy credentials never change gateway when the UI grouping is merged.

## Capability surface (declared in `wbRegistration`)

| Capability | Implementation file | What it does |
|---|---|---|
| `ModelProvider` | `models.go` | Static + dynamic model list, alias reverse-resolution, opt-in plugin `models_enabled` allowlist, `models_disabled` deny override, and CPA `oauth-excluded-models` filters |
| `AuthProvider` | `oauth.go`, `authfile.go`, `main.go` | OAuth login flow (public CN / Intl; separate CodeBuddy Intl and legacy WorkBuddy service routing), token refresh, auth file parse |
| `Executor` | `main.go`, `stream.go`, `payload.go` | Chat completions, streaming SSE pump, request body rewriting |
| `Scheduler` | `scheduler.go`, `active_auth.go` | `builtin` (default) returns `DelegateBuiltin(round-robin)` so CPA's own scheduler routes; `scheduler_mode: credits` opts into panel-selected account routing |
| `ManagementAPI` | `management.go`, `panel.go`, `checkin.go`, `credits_handler.go`, `billing.go`, `config.go`, `host_auth.go` | Dashboard, manual check-in, credits query, import credential, config |
| `RequestLifecyclePlugin` | `main.go`, `request_lifecycle.go` | Request completion observations for terminal alerts |
| `QuotaProvider` | `quota.go`, `billing.go`, `cache.go` | Native CPA quota fetch/describe/reset; fetch is read-only and does not run lifecycle reconcile |
| `UsagePlugin` | `daily_quota.go` | Accumulates per-(account, model, day) free-tier token usage from host usage records; the host still reports usage itself |

The browser panel is two assets: `panel.html` (markup + styles + early theme
bootstrap) and `panel.js` (all logic). The dashboard is advertised with a legacy
GET route and `Menu` label for older CPA hosts; newer hosts map it to the plugin
resource URL. `panel.js` remains an explicit resource route. Resource paths are
matched exactly, so either undeclared asset 404s and the page renders without running.

## File map (by responsibility)

```
main.go              C ABI exports, registration, dispatch, config fields, host calls
host_bridge.go       CPA HTTP bridge, streaming reader, test-only direct helpers
host_auth.go         CPA auth-store RPC helpers
oauth.go             Login, polling, refresh, OAuth client construction
authfile.go          Auth parsing, identity metadata, canonical naming and persistence
billing.go           Billing, check-in and trial upstream requests
models.go            Static catalogs, discovery, cache, aliases and filters
models_handler.go    CPA static / per-auth model registration handlers
chat_error.go        Upstream error classification and 11150 retry
payload.go           Request preparation and rewriting
stream.go            Executor paths, stream pump and response aggregation
management.go        Management routes and request handling
panel.go             Dashboard assembly and panel resource handling
panel.js / panel.html Embedded dashboard UI and model controls
config.go            YAML decoding and runtime plugin configuration
policy.go            Pure lifecycle decisions and account labels
lifecycle.go         Reconcile, disable/retain and lifecycle state
scheduler.go         CPA delegation and optional credits-based selection
request_lifecycle.go Request completion tracking and terminal alerts
quota.go             Native CPA QuotaProvider implementation
daily_quota.go       Per-model daily usage accounting
```

Headers, auth types, and small helpers are kept alongside their consumers.

## Data flow

### Chat completion (streaming)

```
client → CPA → plugin.handleExecStream
  → parseStored(auth file)
  → resolveUpstreamModel(alias → upstream id)
  → prepareUpstreamBody (single JSON pass: forceStream + normalizeTools +
                          rewriteSystem + ensureSystemMessage + rewriteModel)
  → hostHTTPDoStream (via CPA host bridge → request-log captured)
  → on explicit HTTP 400 code 11150 only: remove top-level reasoning effort and retry once via the same bridge
  → pumpUpstreamStream (goroutine)
      → hostStreamReader → bufio.Scanner → SSE lines
      → cleanChunkJSON per line
      → streamEmit → CPA → client
  → invalidateAccountCredits (async)
  → host records usage itself (core usage reporter → redisqueue →
    GET /v0/management/usage-queue ← CPAMP collector pulls this)
  → host calls UsagePlugin.HandleUsage → handleUsage (no-op acknowledgement;
    the plugin pushes nothing to CPAMP — doing so duplicated every request)
```

### Daily check-in (CN, 09:00 / 21:00)

```
schedulerLoop → runAutoCheckin (sem=4 concurrent)
  → processAutoCheckinAccount per account
      → fetchCheckinStatus → performCheckinCall if needed
      → update accountCache (merge, not wipe)
      → reconcileOneAccount → applyExhaustedPolicy
          → policy.go decides: disable (CN) / retain-disabled (legacy WorkBuddy service) / reenable (CN)
          → authfile.go applies changes through hostAuthPersist; the legacy-named deleteAuth retains and disables the record
```

### Dashboard load

```
panel.html → /v0/management/plugins/workbuddy/accounts
  → handleManagement (auth + ratelimit)
  → buildDashboardEx (concurrent cachedAccountDetails per account, sem=4)
      → accountDetailFlight singleflight dedups concurrent fetches
      → accountCache hit → return cached
      → miss → 3 concurrent billing API calls (plan/checkin/credits)
  → summarizeCredits
```

## Key design decisions

1. **Host HTTP bridge for all upstream calls.** Every HTTP request to
   CodeBuddy / CPAMP goes through `host.http.do` / `host.http.do_stream` so
   CPA's request-log captures outbound traffic and host transport policy
   (proxy, timeout) applies. The plugin's own `sharedHTTPClient` is a
   only used by explicit test helpers; production calls fail closed when the host
   bridge is unavailable and are never replayed through a second transport.

2. **Single-flight per account for billing API.** `cachedAccountDetails`
   uses a `sync.Map` of in-flight calls so concurrent dashboard refreshes
   and reconcile ticks for the same account share one upstream fetch
   instead of stampeding the billing API.

3. **Cache merge, never wipe.** All cache writes merge with the previous
   entry (credits + plan + checkin) instead of replacing it. The "early
   already checked in" fast path used to wipe credits/plan; v0.6.31 fixed
   that by always merging.

4. **UID whitelist for auth file names.** `sanitizeUIDForFileName` strips
   any character outside `[a-zA-Z0-9_-]` and caps length at 64, preventing
   path traversal when importing credentials with attacker-controlled UIDs.

5. **Plugin-layer management auth is opt-in.** When `management_key` is
   unset the plugin defers entirely to CPA's management middleware
   (historical default). When set, mutating endpoints accept a constant-time
   match on `X-Management-Key` or `Authorization: Bearer`; only rejected
   credentials consume the per-IP failure bucket.

6. **Native quota is read-only.** `quota.fetch` reuses the billing parser and
   host HTTP transport, but does not disable, re-enable, delete, or otherwise
   reconcile auth lifecycle state. The custom management `/refresh` remains the
   explicit full refresh operation.

7. **Usage is reported by the host, not the plugin.** CPA ≥ v7.2.146 wraps
   plugin executors in its core usage reporter, so every plugin request lands
   in the host usage queue that CPAMP's collector pulls. The plugin's own
   CPAMP import (removed in 0.9.30) duplicated every request — verified live
   on v7.3.7: one chat request produced one host-queue record AND one plugin
   import, and the importer does not dedup them.

6. **Scheduler delegates by default.** `scheduler_mode: builtin` (default)
   explicitly delegates to CPA's built-in round-robin strategy. The legacy
   `off` value remains an alias; the plugin picks accounts only when the
   operator opts in with `scheduler_mode: credits`.

7. **Shutdown drains background work before releasing the host bridge.** The
   plugin shutdown path cancels its root context and waits for workers to exit
   while CPA callbacks are still valid, allowing asynchronous stream cleanup to
   close host streams safely.

8. **Empty upstream answers are rate limits, not successes (0.9.32).** A
   throttled WorkBuddy account answers `200` with well-formed SSE framing but no
   output at all. Counting "at least one valid JSON chunk" as success folded that
   into a normal `content:""` completion, so the user saw an empty reply reported
   as healthy AND the credential stayed in rotation for the next request. All
   three data paths (folded non-stream, sync stream, async pump) now detect the
   framing-only shape via `emptyAnswerError` and surface **429**, which is the
   semantic CPA acts on: cool this credential, retry another account. The
   "has output" test accepts text, reasoning, or tool calls, so a tool-only turn
   is never misclassified. The check runs on the raw JSON *before* the optional
   `data: ` framing is applied — `chunkHasModelOutput` also sees through an
   existing frame, because every cross-format client (Claude/Gemini/Codex)
   receives framed chunks.

9. **A context-free model catalog is throttling, not a catalog (0.9.32).**
   Under load the discovery endpoint still returns `200` and parses, but every
   entry's `contextWindow` is `0`. Caching that (the old behaviour, 5-minute TTL)
   advertised "0 context" models to clients, which then fall back to their own
   defaults and compress context far too early. `discoveryContextDegraded` fires
   only when *every enabled* entry lacks a context window (individual omissions
   are legitimate) and yields a typed `*upstreamError{429}`, so the caller serves
   the realm's static catalog — which carries real context windows — and records
   the reason for the panel. `callModelsAPI` adds a short retry ladder
   (700ms/2s, 5s ceiling) reusing the shared `Retry-After` parsing.

10. **Token counting is a local, high-biased estimate whose WIRE SHAPE is
    dictated by the host translator (0.9.32, corrected 0.9.33).** Upstream has no
    count_tokens endpoint, so `token_count.go` measures the prompt locally. The
    old hardcoded `{"input_tokens":0}` told context-budgeting clients (Claude
    Code calls `POST /v1/messages/count_tokens` every turn) that the prompt was
    free: they never compacted and the real request eventually exceeded the
    upstream limit. The estimator counts only model-visible text (message
    content, system prompts, tool names/descriptions/schemas) and skips protocol
    plumbing (role/type/id/sampling params), divides by 3 to bias high — an
    over-estimate costs mild premature compaction, an under-estimate costs a
    hard failure — and floors at 1 so 0 always means "unknown".

    0.9.32 computed the right number and still delivered 0, because the payload
    shape was wrong. The plugin declares
    `ExecutorOutputFormats=["chat-completions"]`, so the host adapter
    (`internal/pluginhost/adapters_executors.go`, `translateExecutorResponse`)
    translates the plugin's payload with `TranslateNonStream(openai → claude)`.
    That is the openai→claude *response* translator, which reads
    `usage.prompt_tokens` via `extractOpenAIUsage` and ignores a top-level
    `input_tokens` — so the Claude-native shape was silently rewritten to
    `usage.input_tokens: 0`. The host's `TokenCount` hook is only reachable by
    native executors (`ClaudeExecutor` calls `TranslateTokenCount` directly with
    the count as an argument); plugin executors never reach it.

    The wire shape is therefore
    `{"usage":{"prompt_tokens":N,"completion_tokens":0,"total_tokens":N}}` —
    byte-identical to `helps.BuildOpenAIUsageJSON`, the shape every
    openai-compatible native executor returns and hence the best-tested one.
    This is the one place where the plugin's output must follow the host's
    translation contract rather than the client's native schema.
    The token-count wire-shape unit tests guard this contract against regressions;
    the plugin module cannot directly import the host's `internal/` packages.

11. **Account identity must live in the auth file (0.9.32).** The host rebuilds
    an auth record *from the file JSON* on every `host.auth.save`
    (`buildAuthFromFileData`), deriving the row label as `metadata["email"]` with
    the provider key as fallback. Because every persist path in this plugin goes
    through that RPC, writing no identity meant the first lifecycle save
    (disable / note sync / keepalive) relabelled the account as `workbuddy` even
    though the credential still held the nickname. The writer now persists
    `email` / `account_name` / `uid` / `enterprise_id` (only when absent, so an
    operator rename wins), `parseStored` reads them back in both credential
    shapes, and `accountNameForAuth` is the single source of truth shared by the
    label and the metadata.

12. **Scheduling belongs to CPA; the plugin only supplies facts (0.9.34).** The
    default `scheduler_mode` is `builtin`, which returns
    `DelegateBuiltin: "round-robin"` rather than `Handled: false`. The
    distinction matters: `Handled: false` only means "this plugin declines",
    leaving the host's next step unspecified, whereas naming a delegate pins the
    strategy to CPA's own implementation so the plugin cannot drift from it. The
    plugin still owns the panel's "selected account" concept, but that is a UI
    convenience — it is not a routing policy unless an operator explicitly sets
    `scheduler_mode: credits`. Keeping the switch (rather than deleting it)
    preserves that opt-in while making the default unambiguous.

13. **The daily free allowance is a separate budget from credits, and is
    measured, not fetched (0.9.34).** Two unrelated things that are easy to
    conflate:

    | | paid credits | daily free allowance |
    |---|---|---|
    | nature | purchased | free tier |
    | dimension | account-wide total | **per model** |
    | resets | package cycle | **daily** |
    | source | upstream billing API | **measured locally** |

    Upstream CodeBuddy/WorkBuddy publishes no per-model quota endpoint, so "used
    today" cannot be fetched. The host delivers one `UsageRecord` per completed
    request to plugins declaring `UsagePlugin`, so `daily_quota.go` accumulates
    those into `(auth, model, day)` buckets. Three deliberate choices:

    - **Display only.** Exceeding an allowance never makes the plugin reject a
      request. The upstream is the authority on its own free tier, and a local
      counter that drifted (restart, missed record, clock skew) must never be
      able to block a paying request. The upstream's own 402/429 rides the error
      envelope as before, and CPA cools the credential.
    - **No invented limits.** A model absent from the table is reported as
      measured-but-unlimited, not with a guessed cap: a confidently wrong
      percentage is worse than an absent one.
    - **Counters persist.** They live in memory plus an atomic JSON file under
      the host auth dir (`Host.AuthDir` only rides on model-discovery RPCs, so
      `cacheModelAliases` is where the path is learned). Without persistence a
      plugin reload would show a misleading "100% remaining".

    The panel is the only consumer. CPA's native `/quota` page cannot host this:
    it selects renderers from a hardcoded provider list
    (`antigravity`/`claude`/`codex`/`xai`/`kimi`/`devin`/`meta`) and its frontend
    reads none of the backend's `supports_quota` / `quota_provider` /
    `model_quotas` fields, so a WorkBuddy entry would never render there.

## Version alignment baseline

The plugin targets **CPA v7.3.18** (`go.mod`). Alignment policy:

- `sdk/pluginapi` and `sdk/pluginabi` are the ONLY CPA packages a plugin may
  import; everything else is host-internal and off-limits. Both remain append-only
  (`SchemaVersion` is 6 through v7.3.18). The v7.3.13–v7.3.18 series adds optional
  CLI flag declarations and `host.http.operation_open` / `host.http.cancel` RPCs
  (the latter in v7.3.16); WorkBuddy does not use those APIs and continues using the supported `host.http.do` and
  `host.http.do_stream` calls, so no source migration is required. Verify each
  version bump by building and running the full suite against the target tag.
- Behaviour the plugin depends on but cannot import (translation shapes, RPC
  dispatch, scheduler delegation) should be verified against a real CPA checkout,
  not only a local re-implementation. Unit tests in this module cover the plugin's
  declared wire shape; integration runs against the target CPA revision remain
  the final check for host-side translation behavior.

## Integration points with CPA

- **Auth store**: `host.auth.list` / `host.auth.get` / `host.auth.save` —
  plugin never writes auth files directly to disk, always via host RPC.
- **Model registration**: `model.static` / `model.for_auth` RPC, custom static
  entries from the `models` array, plus `oauth-model-alias` /
  `oauth-excluded-models` from host config.
- **Streaming**: `host.stream.emit` / `host.stream.close` — async SSE
  chunks pushed to the client without blocking the executor return.
- **Test seam**: `hostRPCTestOverride` (main.go) replaces the host RPC table in
  tests, so `host.auth.list/get/save` flows (lifecycle, check-in, keepalive) can
  be driven without a live CPA process.
- **Usage**: `usage.handle` RPC — the host calls `UsagePlugin.HandleUsage`
  after every request, having already recorded the same record into its own
  pipeline. The plugin folds the record into its daily free-allowance counters
  (see design decision 12) and always acknowledges: a malformed record must not
  mark the plugin faulty for a request that already succeeded upstream.
- **Management**: `management.register` returns routes under
  `/v0/management/plugins/workbuddy/*` and a panel resource under
  `/v0/resource/plugins/workbuddy/panel`.
- **Scheduler**: `scheduler.pick` RPC — by default the plugin returns
  `Handled: true` with `DelegateBuiltin: "round-robin"` and no `AuthID`, so CPA
  routes with its own scheduler. `DelegateBuiltin` is used instead of
  `Handled: false` because it names the strategy outright: "declines" leaves the
  outcome unspecified, whereas naming the delegate pins behaviour to CPA's
  implementation. Only `scheduler_mode: credits` returns an `AuthID`.
