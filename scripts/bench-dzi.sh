#!/usr/bin/env bash
# Compare wsitools convert --to dzi against vips dzsave on representative
# fixtures, reporting BOTH wall-clock time and peak resident memory
# (macOS /usr/bin/time -l "maximum resident set size"). Manual / not in
# CI. Run via `make bench-dzi`.

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

# run_bench <cmd> -> echoes "<seconds> <peak_mb>" parsed from
# /usr/bin/time -l. Command stdout/stderr are discarded; only the
# rusage report is captured.
run_bench() {
    local cmd="$1" tf secs rss_bytes
    tf=$(mktemp)
    /usr/bin/time -l sh -c "$cmd" >/dev/null 2>"$tf" || true
    secs=$(grep -E ' real' "$tf" | tail -1 | awk '{print $1}')
    rss_bytes=$(grep 'maximum resident set size' "$tf" | tail -1 | awk '{print $1}')
    rm -f "$tf"
    awk -v s="$secs" -v r="$rss_bytes" 'BEGIN{ printf "%s %.0f\n", s, r/1048576 }'
}

printf "%-32s %9s %8s %9s %8s %7s %7s\n" \
    "fixture" "wsi(s)" "wsi(MB)" "vips(s)" "vips(MB)" "t-rat" "m-rat"
printf -- "------------------------------------------------------------------------------------\n"
for fx in "${FIXTURES[@]}"; do
    [ -f "$fx" ] || continue
    name=$(basename "$fx")

    rm -rf "$TMP/wsi-$name.dzi" "$TMP/wsi-${name}_files"
    read -r wt wm < <(run_bench "'$WSI' convert --to dzi -o '$TMP/wsi-$name.dzi' '$fx'")

    rm -rf "$TMP/vips-$name.dzi" "$TMP/vips-${name}_files"
    read -r vt vm < <(run_bench "vips dzsave '$fx' '$TMP/vips-$name' --suffix '.jpeg[Q=85]' --tile-size 256 --overlap 1")

    trat=$(awk -v a="$wt" -v b="$vt" 'BEGIN{ if(b>0) printf "%.2f", a/b; else print "n/a" }')
    mrat=$(awk -v a="$wm" -v b="$vm" 'BEGIN{ if(b>0) printf "%.2f", a/b; else print "n/a" }')
    printf "%-32s %9s %8s %9s %8s %7s %7s\n" "$name" "$wt" "$wm" "$vt" "$vm" "$trat" "$mrat"
done

echo
echo "t-rat / m-rat = wsitools ÷ vips (lower is better for wsitools)."
echo "Memory = peak RSS (maximum resident set size). Set"
echo "OPENTILE_READ_MEMORY_BUDGET=<bytes> to tune wsitools' read-cache budget."
