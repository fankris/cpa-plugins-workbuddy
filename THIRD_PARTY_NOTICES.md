# Third-party notices and source provenance

Independent development build, not an official release or endorsement by CPA,
CPAMC, WorkBuddy Manager, workbuddy2api-panel or Tencent. Original root MIT
LICENSE and copyright notices are retained.

## Source / reference projects

- Base: fankris/cpa-plugins-workbuddy, commit ba6992e30862e83e07b2a0d4e23de6bed6d1eb68; root LICENSE.
- SDK: router-for-me/CLIProxyAPI v8.0.15, a4acc9f752bd46571f737a10c04bf413656ab06b; licenses/CPA-MIT.txt.
- CPAMC v1.25.3, ee79a794526a30c03748a8864a9ac6589a31833b: storage/locale/theme and native UI integration reference; licenses/CPAMC-MIT.txt. Official application assets are not included in this plugin package.
- ithtelab/workbuddy-manager v1.0.79, 14ad60f0629c60851f83c6a6f55a25d7e3f8dfb3: primary design/interaction reference (globals.css, PageHeader, StatCard, SectionTabs, ManagementBar, CommandPalette, confirmation and account/task presentation); licenses/Manager-MIT.txt. No Manager backend/gateway/database is bundled.
- linguo2625469/workbuddy2api-panel, 15a6fc94ecf9936bd4f70e1c503d9bef6aab2ad9: selective functional/presentation reference; licenses/Panel-reference-MIT.txt. No standalone panel backend is bundled.

Repository URLs and pinned versions/checksums are listed in docs/PROVENANCE.json.

## Runtime code included in the artifact

| Dependency | Version | Notice |
|---|---|---|
| CLIProxyAPI SDK | 8.0.15 | licenses/CPA-MIT.txt |
| gopkg.in/yaml.v3 | 3.0.1 | licenses/YAML-LICENSE.txt (upstream MIT/Apache material retained verbatim) |
| Go runtime/toolchain | 1.26.0 | licenses/GO-BSD.txt |
| React | 19.2.0 | licenses/React-MIT.txt |
| React DOM | 19.2.0 | licenses/ReactDOM-MIT.txt |
| React scheduler | 0.27.0 | licenses/Scheduler-MIT.txt |
| lucide-react | 0.468.0 | licenses/Lucide-ISC.txt (upstream composite notice retained verbatim) |

JavaScript legal comments are embedded inline in panel.js. Notice files also
accompany the source and binary packages. Binary module inventory comes from
`go version -m`; npm runtime versions are fixed in package-lock.json and recorded
in docs/PROVENANCE.json. Build/test dependencies (esbuild, TypeScript, React type
definitions, Playwright) are lockfile dependencies, not bundled node_modules.

The Linux binary dynamically links the system C runtime (glibc 2.34+); no libc
binary, C toolchain, Go toolchain, browser, npm registry contents, official host
binary or full CPAMC application is redistributed. This notice review is not a
security vulnerability assessment or legal opinion.
