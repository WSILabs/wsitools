#!/usr/bin/env bash
#
# check-openslide.sh — auto-validate the run-matrix.sh outputs with the OpenSlide
# CLI (the Aperio-ecosystem gold oracle). For every output OpenSlide can open it:
#   - prints level count + per-level downsamples (catches dropped/duplicated levels),
#   - renders the deepest pyramid level to a PNG (catches the "Dimensional mismatch
#     reading JPEG" edge-tile bug and other read failures),
#   - reports associated images (label/macro/thumbnail).
#
# OpenSlide can't read some containers/codecs (DZI/SZI/IFE/OME-TIFF, and novel
# codecs like JPEG-XL/AVIF/WebP, or JPEG2000 in a *generic* TIFF). Those are
# reported N/A — use Bio-Formats / the Iris validator / `wsitools validate` for
# them. A real FAIL is a container OpenSlide *should* read (e.g. a JPEG SVS) that
# errors on open or render.
#
# Usage: scripts/qa/check-openslide.sh         (OUT defaults to /tmp/wsitools-qa)
#
set -uo pipefail
OUT="${OUT:-/tmp/wsitools-qa}"
CASES="$OUT/cases"
DEST="$OUT/openslide"
mkdir -p "$DEST"

command -v openslide-show-properties >/dev/null 2>&1 || { echo "openslide CLI not found (brew install openslide)"; exit 1; }
[[ -d "$CASES" ]] || { echo "no cases dir at $CASES — run run-matrix.sh first"; exit 1; }

# prop FILE KEY — extract a property value (KEY may contain [] brackets).
prop() { openslide-show-properties "$1" 2>/dev/null | grep -F "$2: '" | head -1 | sed "s/.*: '\(.*\)'\$/\1/"; }
# is the open-error an expected OpenSlide limitation (not a wsitools defect)?
expected_na() { grep -qiE "unsupported tiff compression|compression support is not configured|not a file that openslide can recognize|unsupported|cannot read" "$1"; }

pass=0; fail=0; na=0
printf "%-28s  %-9s  %s\n" "OUTPUT" "RESULT" "DETAIL"
printf -- "---------------------------------------------------------------------------\n"

for path in "$CASES"/*; do
  name="$(basename "$path")"
  case "$name" in
    *.dzi|*.szi|*.iris|*.ome.tiff) printf "%-28s  %-9s  %s\n" "$name" "N/A" "OpenSlide can't read this container"; na=$((na+1)); continue ;;
  esac

  # DICOM output is a directory of .dcm — OpenSlide opens it via one instance.
  target="$path"
  [[ -d "$path" ]] && target="$(find "$path" -maxdepth 1 -name '*.dcm' | sort | head -1)"
  [[ -z "$target" ]] && { printf "%-28s  %-9s  %s\n" "$name" "N/A" "no .dcm in dir"; na=$((na+1)); continue; }

  if ! openslide-show-properties "$target" >/dev/null 2>"$DEST/$name.openerr"; then
    if expected_na "$DEST/$name.openerr"; then
      printf "%-28s  %-9s  %s\n" "$name" "N/A" "$(head -1 "$DEST/$name.openerr" | sed 's#.*: ##')"
      na=$((na+1))
    else
      printf "%-28s  %-9s  %s\n" "$name" "OPENFAIL" "$(head -1 "$DEST/$name.openerr")"
      fail=$((fail+1))
    fi
    continue
  fi

  lc="$(prop "$target" openslide.level-count)"; lc="${lc:-1}"
  last=$((lc-1))
  lw="$(prop "$target" "openslide.level[$last].width")"
  lh="$(prop "$target" "openslide.level[$last].height")"
  downs="$(openslide-show-properties "$target" 2>/dev/null | sed -n "s/^openslide.level\[[0-9]*\].downsample: '\(.*\)'\$/\1/p" | awk '{printf "%.0f ",$1}')"
  assoc="$(openslide-show-properties "$target" 2>/dev/null | sed -n "s/^openslide.associated.\([a-z]*\)\..*/\1/p" | sort -u | tr '\n' ',' | sed 's/,$//')"

  rerr="$DEST/$name.readerr"
  if [[ -n "$lw" && -n "$lh" ]] && openslide-write-png "$target" 0 0 "$last" "$lw" "$lh" "$DEST/$name.png" 2>"$rerr"; then
    printf "%-28s  %-9s  L=%s ds=[%s] assoc=[%s]\n" "$name" "OK" "$lc" "${downs% }" "${assoc:-none}"
    pass=$((pass+1))
  else
    printf "%-28s  %-9s  %s\n" "$name" "READFAIL" "$(head -1 "$rerr" 2>/dev/null) (level $last ${lw}x${lh})"
    fail=$((fail+1))
  fi
done

echo
echo "OpenSlide: $pass OK, $fail FAIL, $na N/A.  Rendered PNGs + error logs in $DEST"
echo "Eyeball $DEST/*.png: colours correct, no black/garbled edges, full tissue extent."
[[ "$fail" -gt 0 ]] && exit 1 || exit 0
