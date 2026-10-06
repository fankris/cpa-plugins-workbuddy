# WorkBuddy extension for CPA

**Current source: `v8.0.15-2.0.0-rebuild.14` · 2026-10-06.** CPA is the host and authority; WorkBuddy Manager is a selective workflow reference, not the architecture to transplant.

Uses CPA 8.0.15 stable SDK (ABI 1 / schema 6), with CPAMC v1.25.3 as the UI contract reference. Preserves `/panel` and provider/auth identity. Delivery policy: keep one latest ZIP containing source, Linux amd64 plugin and offline HTML with actual UI screenshots. Temporary artifacts are cleaned after packaging. See [iteration 14](docs/ITERATION-14.md), [validation](docs/VALIDATION.md), and [ownership matrix](docs/FEATURE-MATRIX.md).

## Mobile-first work surface

Compact navigation and summary, always-visible search/actions/pagination, adaptive account cards, optional filters, and bounded dialogs. Desktop lists also use a viewport-bounded layout. See iteration 14 for measured sizes and limits.

## Desktop refinement

Compact actionable summaries, aligned row controls, desktop page sizes, and a two-column account inspector with previous/next navigation. Tested at 1024, 1366, 1440 and 1920px widths in four languages. Mobile behavior remains intact.

## Six workspaces

- **Accounts & plans:** summaries, search/filter/pagination, selection, details, credential import, native enable/disable and credential refresh, plugin-specific credits/check-in/eligible trial actions.
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

Go race **539 PASS / 1 SKIP**, Node **61 PASS**, fixture browser **102 scenarios plus insecure HTTP**, compiled binary/mock-host ABI **8 groups PASS**. Official-host integration was not rerun; no real credentials were used. See [current validation](docs/ITERATION-14.md).


## 最新交付规则 / Latest delivery

参见 [DELIVERY.md](docs/DELIVERY.md)。`make release` 校验当前截图与二进制证据后生成一个 `workbuddy-latest.zip`；校验成功才替换旧包。单独 HTML 为 `deliverables/WorkBuddy-交付说明.html`。

## Billing execution repair

The plugin now hydrates cold account reads, parses both billing shapes, aggregates pages and preserves stale/error semantics. Legacy menu assets are served through CPA management authentication. See docs/ITERATION-14.md. Real accounts remain untested.

## Host chrome adaptation

External title bars no longer cause duplicate padding. Same-origin floating controls use measured corner exclusion; narrow viewports switch to vertical clearance. Cross-origin frames retain conservative fallback. See docs/ITERATION-14.md.

## Toolbar simplification

Removed duplicated plugin-settings and OAuth-login shortcuts and the read-only settings modal. Mobile import is direct; model/task pages have no empty More menu. Host-native capabilities remain intact.

## Floating-host workspace structure

Recognized floating hosts use a desktop navigation band; portrait mobile uses a real section heading and an in-flow bottom navigation row. Wide landscape returns to top navigation. External-header and standalone layouts remain separate; cross-origin fallback remains conservative.

## Official CPAMC reference

Validated against the unmodified, SHA256-pinned CPAMC v1.25.3 release frontend with a fixture backend. Deduplicated refresh only in the recognized official layout, repaired light-theme transitions, and protected modal close controls beneath expanded host menus. No real-backend/account acceptance claimed.

Rebuild 14 adds a credit-expiry dashboard and plugin-only automation settings. Enable `scheduler_mode: credits_expiry` explicitly to prefer eligible accounts with expiring credits; unknown/stale snapshots fall back to CPA. Rich model metadata is upstream-declared, not verified capability or pricing. See ITERATION-14.md.
