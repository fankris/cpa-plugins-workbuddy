#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
cp "$ROOT/Makefile" "$ROOT/VERSION" "$ROOT/README.md" "$ROOT/README_CN.md" "$ROOT/LICENSE" "$TMP/"
cat > "$TMP/fail-go" <<'SCRIPT'
#!/bin/sh
echo "intentional test compiler failure" >&2
exit 23
SCRIPT
chmod +x "$TMP/fail-go"
if make -s -C "$TMP" release GO="$TMP/fail-go" RELEASE_TARGETS=linux/amd64 > "$TMP/log" 2>&1; then
 echo 'FAIL: release swallowed compiler failure'; exit 1
fi
if find "$TMP" -name '*.zip' | grep -q .; then echo 'FAIL: failed build was packaged';exit 1;fi
if grep -q '^built ' "$TMP/log";then echo 'FAIL: false success message';exit 1;fi
echo 'PASS: release fails closed; no package or success message on compiler failure'
