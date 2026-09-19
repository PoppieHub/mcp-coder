#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
manifest="$root/extensions/claude-desktop/manifest.json"
output="$root/dist/mcp-coder.dxt"
stage=$(mktemp -d "${TMPDIR:-/tmp}/mcp-coder-dxt.XXXXXX")

cleanup() {
	"${RM:-rm}" -rf "$stage"
}
trap cleanup EXIT HUP INT TERM

test "$(uname -s)" = "Darwin" || {
	echo "This DXT currently packages a macOS binary." >&2
	exit 1
}
test -f "$manifest"
mkdir -p "$stage/bin" "$(dirname -- "$output")"
cp "$manifest" "$stage/manifest.json"

(
	cd "$root"
	CGO_ENABLED=0 go build -trimpath -o "$stage/bin/mcp-coder" ./cmd/mcp-coder
)
chmod 755 "$stage/bin/mcp-coder"

rm -f "$output"
(
	cd "$stage"
	zip -q -r "$output" manifest.json bin
)
unzip -t "$output" >/dev/null
echo "Created $output"
