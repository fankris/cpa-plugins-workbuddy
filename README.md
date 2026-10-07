# WorkBuddy extension for CPA

**v8.0.15-2.0.0-rebuild.21 · 2026-10-07**. CPA8.0.15 SDK, ABI1/schema6. Original WorkBuddy identity, menu and `/panel` are preserved.

## This iteration

A model-first workspace: compact All/Dynamic/Custom filters, search, channels and sorting. Source accounts move to an on-demand dialog; technical diagnostics are collapsed. Unsupported channels no longer generate a generic partial-fetch warning. Actual failures name the affected channel without opening dialogs automatically.

Same-ID models retain all source variants. Displayed values use the first visible variant; differences carry a labelled channel/origin basis and remain available in details. Source selection never changes CPA routing; observing a directory never registers callable models.

Five workspaces remain: Dashboard, Accounts, Models, Tasks and plugin-only Settings. Preserve Manager realm parsing, expiry checks, dual-source fetching, scoped host HTTP, CPA-native config confirmation/readback and previous business behavior.

See [Chinese guide](README_CN.md), [iteration21](docs/ITERATION-21.md), [current validation](docs/VALIDATION-21.json) and [ownership](docs/FEATURE-MATRIX.md).

Build with Go1.26, a C compiler and Node/npm: `make frontend`, `make frontend-test`, `make test`, `make build`. The single latest ZIP includes source, Linux amd64/glibc≥2.34 plugin, checksums and an offline HTML with 26 actual UI captures.

No production changes or real-account acceptance. Screenshots and browser tests use synthetic data; original CPAMC frontend and mock C ABI host are not complete CPA-backend acceptance. Test success does not imply user approval of the UI. CodeBuddy International dynamic directory remains unverified.
