# WorkBuddy CPA plugin — Manager-led rebuild

**Independent development build `v8.0.15-2.0.0-rebuild.1` · 2026-10-05**

[完整中文说明](README_CN.md) · [Validation](docs/VALIDATION.md) · [Feature mapping](docs/FEATURE-MATRIX.md) · [Provenance](docs/PROVENANCE.json)

Based on fankris/cpa-plugins-workbuddy, with a new React/TypeScript interface informed primarily by WorkBuddy Manager 1.0.79. This is not a Manager gateway/backend transplant, an official upstream release, or an endorsement by the referenced projects.

Targets the verified stable CPA **8.0.15** SDK, ABI schema **6**; actually loaded and browser-tested with official CPA 8.0.15 and CPAMC **1.25.3**. Preserves the WorkBuddy provider/auth identity, menu and `/panel` resource. Included binary: **Linux amd64, glibc 2.34+ only**.

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

Final Go race: **506 named tests/subtests**; Node: **28**; fixture browser: **12**; real official host/browser: **12**, all passed. Actual C ABI registration, routes, middleware, native config, remembered-key inheritance and native four-language iframe recreation were tested.

**Real account inference, real credential refresh, rewards/check-in/trial/travel were not exercised.** Empty-host integration and fixture success are not real-account E2E acceptance. See `docs/VALIDATION.md` and `THIRD_PARTY_NOTICES.md`. Historical development notes are excluded from release archives.

One optional wire-shape export helper was skipped because WORKBUDDY_WIRESHAPE_OUT was not set; it is not included in the 506 passes.
