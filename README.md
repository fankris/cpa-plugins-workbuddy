# WorkBuddy extension for CPA

**Current source: `v8.0.15-2.0.0-rebuild.20` · 2026-10-07.** CPA is the host and authority. WorkBuddy Manager is a selective workflow reference, not a standalone architecture to transplant.

CPA8.0.15 SDK, ABI1/schema6. Original provider/auth identity, WorkBuddy menu and `/panel` are preserved. One latest combined ZIP contains source, Linux amd64 plugin, illustrated offline HTML, licenses and checksums.

## This iteration

Fix Manager auth.realm preservation, expired directory source selection, envelope and candidate path compatibility. Show per-channel failures and sanitized request diagnostics. Real-account recovery is not yet verified.

Unified model hub: one observational source account per channel, all fetched and explicitly custom-defined models, exact-ID aggregation, independent provenance/channel badges and per-source metadata. Changing directory sources never selects the CPA routing account. Native model configuration remains available within details with confirmed readback.

Preserved from iteration18: Remove the Results workspace and its history/filter/export UI. Put connection information on Dashboard. Preserve per-action success/failure/uncertainty feedback and redacted details in the relevant business page. Old `#results` links and same-origin host-memory selections migrate to Dashboard; retired history keys are cleared. CPA native logs and task/server state are not deleted.

Five workspaces: **Dashboard, Accounts, Models, Tasks, Settings**. Connection information reuses the account read; it adds no polling. The SDK value is the build target, not a measured host version. v8/v0 is the compatibility range, not proof that both routes work.

See [Chinese guide](README_CN.md), [iteration20](docs/ITERATION-20.md), [validation](docs/VALIDATION-20.json) and [ownership](docs/FEATURE-MATRIX.md).

## Preserved behavior

- Concurrent account-scoped enterprise + `/v3/config` directory, full metadata, source conflicts and honest failure states. Directory entries never automatically register CPA routes. CodeBuddy International dynamic directory remains unverified.
- CPA configuration read-only v8/v0 negotiation, pinned writes/readback and no mutation replay.
- Earliest-expiring credit batch and one-decimal day countdown; server-calibrated snapshot, no extra billing polling.
- Plugin-only business settings and optional expiry-first selection within eligible same-priority CPA candidates, not control of package debit order.
- Four locales, native host layout, compact mobile navigation and desktop lists. No duplicate auth store, gateway, login system or generic configuration editor.

## Safety and migration

The host owns auth persistence, logging, HTTP and streaming. Scoped parent-memory view state preserves filters and unsubmitted drafts across same-origin iframe remounts; drafts are not stored in localStorage/sessionStorage. Confirmations are never replayed. Native refresh responses are discarded in favor of safe summaries.

Default scheduler mode is `host`; explicit `builtin` and historical `off` still delegate round-robin, and off does not disable the plugin. `credits` means selected account, not highest balance. Token keepalive/travel default false; check-in/lifecycle retain true defaults, so explicitly disable before first loading if unwanted. Synthetic activity remains disabled. Legitimate tasks remain, without fabricated completion or quota evasion.

Native GET/merge/PUT preserves unknown fields but is not atomic across editors. Cancellation cannot undo already issued upstream actions.

## Build and isolated installation

Go1.26, C compiler, Node/npm. Embedded frontend assets are included; rebuild whenever frontend source changes:

```sh
make frontend
make frontend-test
make test
make build
python3 tests/preview_server.py --bind 0.0.0.0 --port 8080
```

Browser tests need Playwright Chromium/system libraries. Preview data is synthetic.

Back up the existing plugin/config/auth data. Load workbuddy.so through the native plugin manager in an isolated CPA8.0.15 instance first. The package is Linux amd64/glibc≥2.34, not ARM/Windows/musl. Rollback restores your own binary/config backup and cannot reverse upstream business actions.

## Acceptance boundary

Go race567 PASS / 1 optional SKIP; vet, TypeScript/build PASS; Node102; browser195 groups + plainHTTP, including94 original CPAMC1.25.3 frontend groups; binary/mock-host ABI11. The HTML embeds37 current screenshots,20 using the original official frontend. Backend data is synthetic throughout. No real Tencent account, user CPAMP deployment or production acceptance is claimed. Production was not changed.
