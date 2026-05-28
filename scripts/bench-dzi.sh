#!/usr/bin/env bash
# Compare wsitools convert --to dzi against vips dzsave on representative
# fixtures. Manual / not in CI. Run via `make bench-dzi`.

set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WSI="$ROOT/bin/wsitools"
SAMPLES="${WSI_TOOLS_TESTDIR:-$HOME/GitHub/opentile-go/sample_files}"
TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT

if ! command -v vips >/dev/null; then
    echo "vips not in PATH; install libvips and re-run." >&2
    exit 1
fi
if [ ! -x "$WSI" ]; then
    echo "$WSI not built; run 'make build' first." >&2
    exit 1
fi

FIXTURES=(
    "$SAMPLES/svs/CMU-1-Small-Region.svs"
    "$SAMPLES/ndpi/CMU-1.ndpi"
    "$SAMPLES/ndpi/OS-2.ndpi"
)

run_timed() {
    local cmd="$1"
    local start end
    start=$(date +%s.%N)
    eval "$cmd" >/dev/null 2>&1
    end=$(date +%s.%N)
    awk -v s="$start" -v e="$end" 'BEGIN{ printf "%.2f\n", e - s }'
}

printf "%-44s %12s %12s %8s\n" "fixture" "wsitools(s)" "vips(s)" "ratio"
printf -- "----------------------------------------------------------------------------\n"
for fx in "${FIXTURES[@]}"; do
    if [ ! -f "$fx" ]; then
        continue
    fi
    name=$(basename "$fx")

    rm -rf "$TMP/wsi-$name.dzi" "$TMP/wsi-${name}_files"
    t1=$(run_timed "'$WSI' convert --to dzi -o '$TMP/wsi-$name.dzi' '$fx'")

    rm -rf "$TMP/vips-$name.dzi" "$TMP/vips-${name}_files"
    t2=$(run_timed "vips dzsave '$fx' '$TMP/vips-$name' --suffix '.jpeg[Q=85]' --tile-size 256 --overlap 1")

    ratio=$(awk -v a="$t1" -v b="$t2" 'BEGIN{ if(b>0) printf "%.2fx", a/b; else print "n/a" }')
    printf "%-44s %12s %12s %8s\n" "$name" "$t1" "$t2" "$ratio"
done
