# WorkBuddy extension for CPA

**Current source: `v8.0.15-2.0.0-rebuild.7` · 2026-10-05.** CPA is the host and authority; WorkBuddy Manager is a selective workflow reference, not the architecture to transplant.

Uses CPA 8.0.15 stable SDK (ABI 1 / schema 6), with CPAMC v1.25.3 as the UI contract reference. Preserves `/panel` and provider/auth identity. Delivery policy: keep one latest ZIP containing source, Linux amd64 plugin and offline HTML with actual UI screenshots. Temporary artifacts are cleaned after packaging. See [iteration 5](docs/ITERATION-7.md), [validation](docs/VALIDATION.md), and [ownership matrix](docs/FEATURE-MATRIX.md).

## Mobile-first work surface

Compact navigation and summary, always-visible search/actions/pagination, adaptive account cards, optional filters, and bounded dialogs. Desktop lists also use a viewport-bounded layout. See iteration 7 for measured sizes and limits.

## Desktop refinement

Compact actionable summaries, aligned row controls, desktop page sizes, and a two-column account inspector with previous/next navigation. Tested at 1024, 1366, 1440 and 1920px widths in four languages. Mobile behavior remains intact.

## Four workspaces

- **Accounts & plans:** summaries, search/filter/pagination, selection, details, credential import, native OAuth entry, native enable/disable and credential refresh, plugin-specific credits/check-in/eligible trial actions.
- **Model diagnostics:** catalog, per-account discovery, discovery refresh and persistent model switches through native plugin configuration, preserving opaque fields.
- **Tasks & activities:** legitimate CN task state, accept actions, completed reward claims, accept-only background batches, status polling/cooperative cancellation and travel operations.
- **Diagnostics & results:** truthful outcomes including partial/unknown/cancel-requested; redacted export; native logs; local usage-counter reset explicitly does not restore upstream quota.

Most business functions predate this rebuild. The new work is UI replacement, host integration and correctness improvements, not a claim that all listed capabilities are newly invented.

Host auth/config/logging/usage/HTTP/streaming are reused; no separate account database, gateway or generic scheduler is added. Plugin settings editing delegates to CPAMC. Plugin-specific caches and business-task state remain local.

## Locale and sensitive state

Same-origin CPAMC locale/theme follows the host, with zh-CN, zh-TW, en and ru. CPAMC 1.25.3 recreates the iframe on language changes: a scoped parent-memory snapshot preserves drafts, filters and task views. Import drafts are not written to localStorage or sessionStorage and disappear when the parent page reloads/tab closes. No invented cross-origin messaging bridge. A missing inheritable management key requires manual entry (tab sessionStorage); host unremembered keys are not scraped. Confirmation callbacks are never replayed; interrupted actions without a server run ID restore as unconfirmed.

## Migration warnings

- Omitted `scheduler_mode` now defaults to `host`. Explicit `builtin` and historical `off` still delegate round-robin; **off does not disable requests**. `credits` selects the active account, not highest balance.
- Omitted `token_keepalive` and `travel_auto` default false. Native CPA credential refresh remains CPA-owned.
- `growth_auto` is forced false; synthetic `/tasks/light` returns HTTP 410. Legitimate task functions remain. No fabricated activity, false completion, registration abuse or quota evasion.
- `checkin_auto` and `lifecycle_auto` retain true defaults. Explicitly disable these in native configuration before loading if automatic business actions are unwanted.
- Native config GET/merge/PUT preserves unknown fields but is **not atomic between independent editors**.
- Cancellation stops future work, not completed/in-flight upstream actions. Task state is in memory; terminal snapshots expire after approximately 15 minutes.

## Build and isolated installation

Requires Go 1.26.0, a C compiler and Node/npm. `make frontend` installs pinned dependencies, typechecks and rebuilds embedded assets. `make frontend-test`, `make test` and `make build` run contracts, Go race tests and the current-platform c-shared build. Existing assets are included in source; rebuild them whenever frontend source changes.

Back up the existing plugin/config/auth data. Use the official host's configured plugin directory and native plugin management to load `workbuddy.so` into an isolated CPA 8.0.15 first. Confirm schema 6 registration and the existing CPAMC `/panel` resource. Do not rename auth files or add another gateway. Roll back by restoring the binary/config backup and restarting; this cannot undo upstream mutations already issued. No production deployment was performed.

For fixture preview: `python3 tests/preview_server.py --port 8080`; then `node tests/rebuild-browser.mjs` with Playwright Chromium/system libraries installed. Fixture data is visibly labeled and is not real account evidence. Real-host integration additionally requires the official binaries/assets and an isolated loopback host; see validation instructions.

## Acceptance boundary

Go race **533 PASS / 1 SKIP**, Node **61 PASS**, fixture browser **62 scenarios plus insecure HTTP**, compiled binary/mock-host ABI **8 groups PASS**. Official-host integration was not rerun; no real credentials were used. See [current validation](docs/ITERATION-7.md).


## 最新交付规则 / Latest delivery

参见 [DELIVERY.md](docs/DELIVERY.md)。`make release` 校验当前截图与二进制证据后生成一个 `workbuddy-latest.zip`；校验成功才替换旧包。单独 HTML 为 `deliverables/WorkBuddy-交付说明.html`。
