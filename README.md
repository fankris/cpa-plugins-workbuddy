# WorkBuddy extension for CPA

**v8.0.15-2.0.0-rebuild.23 · 2026-10-07** — CPA8.0.15 SDK, ABI1/schema6. Original WorkBuddy identity, menu and `/panel` preserved.

## Current iteration
Only two account/model regions: **CN and international**. WB and CB are entrypoints, not separate account channels. Existing-account business requests default to the WB entrypoint. CB international credentials are no longer excluded from dynamic model discovery. Legacy source keys and custom model settings remain compatible.

Credentials, physical names, historical adoption/maintenance policies and actual CPA routing remain independently owned; changing a model observation source never changes routing. Existing native login integration remains intact. Domestic WB APIs legitimately use copilot.tencent.com and codebuddy.cn; no guessed workbuddy.cn endpoints.

Five workspaces remain. Preserve rebuild22 scoped credit reads vs confirmed whole-account maintenance, tasks, settings and native config confirmation/readback. Model metadata retains per-source evidence, not a claim of successful inference.

See [Chinese guide](README_CN.md), [iteration23](docs/ITERATION-23.md), [current validation](docs/VALIDATION-23.json) and [ownership](docs/FEATURE-MATRIX.md).

Go1.26, C compiler, Node/npm: `make frontend`, `make frontend-test`, `make test`, `make build`. The one latest ZIP contains source, Linux amd64/glibc≥2.34 plugin, checksums and an offline HTML with 26 current UI captures. Synthetic tests only; no real Tencent-account acceptance and no production changes.
