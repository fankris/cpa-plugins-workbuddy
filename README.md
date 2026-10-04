# WorkBuddy Plugin for CLIProxyAPI

A [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) plugin that
provides **Tencent CodeBuddy / WorkBuddy** as one native OAuth provider. It
automatically routes each credential to the correct service: domestic CN
(`copilot.tencent.com` / `codebuddy.cn`) or Intl (`codebuddy.ai` and legacy
`workbuddy.ai`). The panel and account labels expose only CN and Intl; the
legacy WorkBuddy service keeps its own chat, billing, refresh, model-discovery,
and account-behavior routing. The plugin also provides dynamic model discovery
where supported, a streaming executor, credit-aware scheduling, daily check-in
automation, and a built-in management dashboard.

[中文文档 → README_CN.md](README_CN.md)

## Features

- **OAuth login** — multi-account auth files via the host's auth store. New
  credentials use service-qualified names: `workbuddy-CN-<uid>.json`,
  `workbuddy-Global-<uid>.json` for legacy `workbuddy.ai`, and
  `workbuddy-intl-<uid>.json` for `codebuddy.ai`. Global is no longer a public
  region: both overseas services display as Intl while remaining separate for
  routing and identity. Existing legacy names stay readable; migrations never
  delete host-owned records.
- **Realm-aware models** — CN and legacy WorkBuddy use their service model APIs
  with 5-minute caches; CodeBuddy Intl uses its static catalog or an explicit
  `models_intl` pin. The `models` custom static array adds user-defined entries
  by ID, name, channel, context, max tokens, and enabled state; use `channel: cn`
  for CN or `channel: intl_global` for either Intl service. Old `global`/`intl`
  configuration values remain readable. Entries are not upstream-validated
  and can produce 11102 if the account cannot use them. The global model-disable
  list is `models_disabled`; manage it in Settings → Model Management. It filters
  both CPA model-registration paths. Host-side `oauth-model-alias` and `oauth-excluded-models`
  continue to apply independently.
- **Executor** — OpenAI-compatible chat completions, both streaming (real SSE
  via `host.stream.emit`) and non-streaming (SSE folded into a single
  completion). `tool_choice` normalization, Claude Code template sanitization,
  and per-realm system-message injection are built in.
- **Credit lifecycle** — CN accounts auto-`disabled` when credits run out and
  re-enabled when a check-in restores them. Eligible legacy WorkBuddy-service
  accounts are retained and marked `disabled` on exhaustion (one-shot trial
  quota); final cleanup remains a CPA management operation. Hard credit errors
  from the executor trigger an immediate reconcile.
- **Daily check-in** — CN accounts are checked in at 09:00 and 21:00 local
  time (configurable). Manual "check in all" from the panel. Per-account
  mutex prevents duplicate claims from racing browser tabs.
- **Trial claim** — eligible legacy WorkBuddy-service accounts can claim the
  one-time expert trial pack from the panel; CodeBuddy Intl is not redirected to
  the WorkBuddy trial endpoint.
- **Dashboard** — embedded panel at `/v0/resource/plugins/workbuddy/panel`
  with credits progress bars, plan badges, exhausted/disabled flags, CN/Intl
  filtering, persistent global model-disable controls, and credential import.
- **Scheduler** — `builtin` (default) hands routing to CPA's built-in
  scheduler by returning `DelegateBuiltin(round-robin)`, which pins the
  strategy to CPA's own implementation rather than merely declining to choose.
  `scheduler_mode: credits` opts into the plugin picking the panel-selected
  account instead.
- **Host scheduling state** — the panel shows what CPA actually routes on
  (cooldown remaining, availability, priority, success/failure counts) from
  `host.auth.get_runtime`, instead of the plugin's own guess.
- **Request terminal alerts** — consumes CPA's `request.complete` events and
  warns in the panel and log after 5 consecutive unsuccessful requests. This is
  the only place "traffic arrived but nothing served it" is visible.
- **Daily free allowance** — the panel shows per-model free-tier usage
  (e.g. `deepseek-v4.1-flash` 200M tokens/day) measured locally from host
  usage records, since upstream publishes no per-model quota endpoint.
  Separate from purchased credits: per model, reset daily, display only —
  exceeding it never blocks a request.
- **Native quota refresh** — implements CPA's `QuotaProvider`, so native quota
  fetch/refresh calls query WorkBuddy billing and expose subscription, aggregate,
  and package windows. Native reset is reported as unsupported; the custom
  `/v0/management/plugins/workbuddy/refresh` remains available for the full
  dashboard/lifecycle refresh.
- **Usage forwarding** — none needed: the CPA host records this plugin's
  requests into its own usage queue (`/v0/management/usage-queue`) and CPAMP's
  collector pulls it. The plugin's own direct push was removed in 0.9.30
  (it stored two rows per request in CPAMP).

## Quickstart

### 1. Install the plugin

Drop the compiled `workbuddy.so` into CPA's plugin directory:

```bash
cp workbuddy.so /path/to/cliproxyapi/plugins/
```

For multi-arch deployments use the platform subdirectory convention:

```
plugins/
  linux/amd64/workbuddy.so
  linux/arm64/workbuddy.so
  darwin/arm64/workbuddy.so
```

### 2. Enable in `config.yaml`

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    workbuddy:
      enabled: true
```

### 3. Sign in

Open the WorkBuddy panel from CPA's sidebar (or hit
`/v0/resource/plugins/workbuddy/panel` directly) and click **登录** to start
the OAuth flow. Repeat for each account you want to add — the plugin writes a
service-qualified file (`workbuddy-CN-<uid>.json`, `workbuddy-Global-<uid>.json`
for legacy `workbuddy.ai`, or `workbuddy-intl-<uid>.json` for `codebuddy.ai`) per account.

### 4. Use it

Call the OpenAI-compatible endpoint with any alias that maps to a workbuddy
model:

```bash
curl http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer $CPA_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "point/deepseek-v4.1-flash",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": true
  }'
```

## Configuration

The CPA native plugin form exposes only `login_region` and `login_platform`.
Other plugin settings remain supported in advanced YAML under
`plugins.configs.workbuddy`; hidden settings are not overwritten when the form
saves. Models are globally disabled by default; Settings → Model Management persists checked IDs in the `models_enabled` allowlist. `models_disabled` remains an explicit deny override.

All fields are optional and live under `plugins.configs.workbuddy`.

```yaml
plugins:
  configs:
    workbuddy:
      enabled: true

      # Only two fields are exposed in CPA's native plugin form:
      # login_region: cn | intl (new logins only)
      # login_platform: CLI | ide (new logins only)

      # Models are opt-in globally: only checked IDs are registered across accounts.
      # Missing/empty models_enabled means all models stay disabled.
      # models_enabled: [deepseek-v4.1-flash, model-to-enable]
      # Optional deny override; matching is exact and case-insensitive.
      # models_disabled: [model-to-hide]

      # Daily check-in automation for CN accounts (default true).
      # Runs at 09:00 and 21:00 local time.
      checkin_auto: true

      # Credit lifecycle: disable CN and retain exhausted legacy WorkBuddy auth,
      # re-enable CN after check-in restores credits (default true).
      lifecycle_auto: true

      # Scheduler behavior (default "builtin"):
      #   builtin → hand routing to CPA's built-in scheduler. The plugin
      #             returns DelegateBuiltin (round-robin), which pins the
      #             strategy to CPA's own implementation instead of merely
      #             declining to choose. "off" is accepted as an alias.
      #   credits → plugin picks the panel-selected account (with fallback
      #             when that account is exhausted / disabled)
      scheduler_mode: "builtin"

      # Daily FREE allowance per model, shown in the panel. Separate from
      # purchased credits: this is the free tier, per model, reset daily.
      # Measured locally (upstream has no per-model quota endpoint), so the
      # panel labels it 本机统计. Display only — exceeding it never blocks a
      # request; the upstream's own rejection is what stops traffic.
      # Built-in default: deepseek-v4.1-flash (200M tokens/day). Override or
      # add models here; 0 removes an override.
      # Accepts 200000000, 200_000_000, 200m, 2e8, 2.5亿, 5000万.
      # Only models with a KNOWN free tier belong here: a wrong entry renders
      # a percentage for a budget that does not exist.
      # daily_free_limits:
      #   deepseek-v4.1-flash: 2亿

      # Usage monitoring needs no plugin configuration: the CPA host records
      # this plugin's requests into its own usage queue
      # (/v0/management/usage-queue), which CPAMP's collector pulls. The old
      # plugin-side direct push to CPAMP was removed in 0.9.30 (it stored two
      # rows per request).

      # Custom static models organized by channel (recommended):
      # intl_global for both Intl services; cn for CN. Use IDs not already in
      # the built-in catalogs; supports ID strings or full model objects.
      # Flat list and legacy realm: global/intl remain compatible.
      models:
        intl_global:
          # - custom-intl-model
        cn:
          # - custom-cn-model

      # Set models_intl to a comma-separated list to pin the complete Intl
      # catalog; leave empty to use the built-in Intl static catalog.
      models_intl: ""
```

Config parsing (0.9.31): settings are read through a YAML parser, so inline
comments, quoting and nesting behave the way YAML users expect — e.g.
`checkin_auto: true # note` is true (the old line-based parser read it as
false, and `login_region: "intl" # note` fell back to cn).

Host model aliases and exclusions remain native CPA config
(`oauth-model-alias` / `oauth-excluded-models`). The panel's `models_enabled`
allowlist controls which models are registered; `models_disabled` is an explicit
deny override. Both filters apply to `model.static` and `model.for_auth` responses.

## Lifecycle

| State | CN account | Intl / CodeBuddy | Intl / legacy WorkBuddy |
|---|---|---|---|
| Credits > 0 | active | active | active |
| Credits = 0 | `disabled: true` (auth file kept) | `disabled: true` | `disabled: true` (auth record kept) |
| Check-in restores credits | re-enabled | n/a | n/a (trial exhausted) |
| Trial available | n/a | n/a | claimable once per account |
| Unknown credits | untouched (never mis-kill) | untouched | untouched |

Hard credit errors from the executor (status 402, "insufficient credits",
"积分不足", etc.) trigger an immediate reconcile of the failing account.

### Rate limiting and empty answers (0.9.32)

A throttled account shows up in two ways, and both are now classified as the
rate limit they are — CPA cools the credential and routes the retry to another
account:

| Symptom | Before | Now |
|---|---|---|
| HTTP 200 with valid SSE framing but **no content at all** (only `role`/`finish_reason`/`usage`) | Folded into a normal `content:""` completion reported as success; the account stayed in rotation | Classified as throttling → **429** `rate_limit_error` |
| Model directory returns `contextWindow` **0 for every entry** | Cached as a real catalog for 5 minutes and advertised (clients fall back to their own defaults and compress context far too early) | Classified as throttling → falls back to the realm's static catalog, which carries real context windows, and records the reason |

Details:

- "Has content" means any of text, reasoning, or tool calls — so a tool-only
  turn (empty `content` with `tool_calls`) is never misclassified.
- `count_tokens` no longer returns 0 either: upstream has no equivalent endpoint,
  so the plugin estimates locally (characters / 3, deliberately biased high, no
  upstream call and no credits spent). Returning 0 told context-budgeting clients
  (Claude Code) the prompt was free, so they never compacted and the real request
  then exceeded the upstream limit.
  The payload shape is the host's, not the client's: because the plugin declares
  `chat-completions` output, the host translates the answer `openai → claude`
  with the openai→claude *response* translator, which reads
  `usage.prompt_tokens` and silently drops a top-level `input_tokens`; this is
  covered by the token-count wire-shape unit tests.
- The directory guard only fires when **every enabled entry** lost its context
  window; individual omissions are legitimate and stay accepted.
- The 429 cooldown is a temporary exponential backoff (10s floor, capped) that
  any successful request clears. The plugin's credit lifecycle deliberately
  ignores soft rate limits, so throttling never disables an account.

### Account names (0.9.32)

An account's display name resolves as nickname → the token's `email`/`upn`/`sub`
claim, and `buildAuthFileJSONFromExisting` persists it as `email` /
`account_name` / `uid` in the auth file. This is required because of the host's
save semantics: `host.auth.save` rebuilds the auth record **from the file
JSON**, taking the row label from `metadata["email"]` and falling back to the
provider key (`workbuddy`). Before 0.9.32 the plugin wrote none of these, so the
first lifecycle write (disable, note sync, keepalive) relabelled the account as
`workbuddy`.

- Fields are written only when absent, so an operator rename in CPA management
  always wins.
- `parseStored` reads them back (both nested and flat credential shapes), so a
  re-read never loses the name.

## Development

Requires Go 1.26+ (matches CPA).

```bash
# Build the plugin
go build -buildmode=c-shared -o workbuddy.so .

# Run tests
go test -race ./...

# Lint
gofmt -l .
go vet ./...
```

The plugin uses CPA's host HTTP bridge (`host.http.do` / `do_stream`) for
all upstream calls so request-log captures outbound traffic and host
transport policy applies. If the bridge is unavailable or returns an
ambiguous result, the plugin fails closed and does not replay the request
through a second transport.

See [docs/development.md](docs/development.md) for the full workflow and
[docs/architecture.md](docs/architecture.md) for the module map.

## License

MIT — see [LICENSE](LICENSE).
