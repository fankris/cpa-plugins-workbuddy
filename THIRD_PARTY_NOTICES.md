# Sources and attribution

This development branch is based on fankris/cpa-plugins-workbuddy commit
ba6992e30862e83e07b2a0d4e23de6bed6d1eb68. The original MIT LICENSE remains intact.

The language integration follows the storage format and locale-selection behavior
of router-for-me/Cli-Proxy-API-Management-Center commit
ee79a794526a30c03748a8864a9ac6589a31833b, specifically src/utils/language.ts,
src/utils/constants.ts, src/stores/useLanguageStore.ts and src/i18n/index.ts.
Its MIT notice is retained at licenses/CPAMC-MIT.txt.

The new plain-text translations and browser adapter are maintained in panel-i18n.js.
This is an independent development build, not an official release or endorsement
by CPA, CPAMC, WorkBuddy Manager or Tencent. No Manager or workbuddy2api-panel backend
has been copied into this development batch.

Build dependencies used for the development binary:
- CLIProxyAPI v8.0.13 SDK: licenses/CPA-MIT.txt.
- gopkg.in/yaml.v3 v3.0.1: licenses/YAML-LICENSE.txt.
- Go 1.26.0 runtime/toolchain notice: licenses/GO-BSD.txt.
The binary dynamically links the system C runtime; no C runtime binaries are bundled.
