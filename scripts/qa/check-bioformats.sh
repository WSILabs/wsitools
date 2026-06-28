#!/usr/bin/env bash
#
# check-bioformats.sh — auto-validate run-matrix.sh outputs with Bio-Formats
# (showinf). Bio-Formats is what QuPath uses under the hood, so a clean parse
# here is a good predictor of QuPath behaviour. For each readable output it:
#   - parses metadata only (showinf -nopix) — catches structural/IFD/OME-XML errors,
#   - reports series count + dimensions,
#   - optionally decodes a small crop (bfconvert) to confirm pixels read.
# Bio-Formats can't read DZI/SZI/IFE — those are reported N/A.
#
# Usage: scripts/qa/check-bioformats.sh [--pixels]   (OUT defaults to /tmp/wsitools-qa)
#   --pixels   also bfconvert a 256x256 crop of series 0 (slower; confirms decode)
#
set -uo pipefail
OUT="${OUT:-/tmp/wsitools-qa}"
CASES="$OUT/cases"
DEST="$OUT/bioformats"
PIX=0; [[ "${1:-}" == "--pixels" ]] && PIX=1
mkdir -p "$DEST"

command -v showinf >/dev/null 2>&1 || { echo "Bio-Formats 'showinf' not found (install bftools)"; exit 1; }
[[ -d "$CASES" ]] || { echo "no cases dir at $CASES — run run-matrix.sh first"; exit 1; }

pass=0; fail=0; na=0
printf "%-28s  %-8s  %s\n" "OUTPUT" "RESULT" "DETAIL"
printf -- "---------------------------------------------------------------------------\n"

for path in "$CASES"/*; do
  name="$(basename "$path")"
  case "$name" in
    *.dzi|*.szi|*.iris) printf "%-28s  %-8s  %s\n" "$name" "N/A" "Bio-Formats can't read this container"; na=$((na+1)); continue ;;
  esac
  # DICOM output is a directory of .dcm — point Bio-Formats at one instance.
  target="$path"
  if [[ -d "$path" ]]; then
    target="$(find "$path" -maxdepth 1 -name '*.dcm' | head -1)"
    [[ -z "$target" ]] && { printf "%-28s  %-8s  %s\n" "$name" "N/A" "no .dcm in dir"; na=$((na+1)); continue; }
  fi

  log="$DEST/$name.showinf.log"
  showinf -nopix -no-upgrade "$target" >"$log" 2>&1
  rc=$?
  # Novel codecs (AVIF/HTJ2K/JPEG-XL/WebP in TIFF) have no Bio-Formats codec —
  # that's an expected reader limitation (same as OpenSlide N/A), not a defect.
  if grep -qiE "Unable to find TiffCompres|unsupported compression" "$log"; then
    printf "%-28s  %-8s  %s\n" "$name" "N/A" "Bio-Formats has no codec for this tile compression"
    na=$((na+1)); continue
  fi
  if [[ $rc -ne 0 ]] || grep -qiE "exception|cannot read|error reading|unsupported" "$log"; then
    printf "%-28s  %-8s  %s\n" "$name" "PARSEFAIL" "$(grep -iE 'exception|cannot|error|unsupported' "$log" | head -1)"
    fail=$((fail+1)); continue
  fi
  series="$(sed -n 's/^Series count = \([0-9]*\)/\1/p' "$log" | head -1)"
  dims="$(grep -m1 'Width = ' "$log" | sed 's/.*Width = //')x$(grep -m1 'Height = ' "$log" | sed 's/.*Height = //')"

  detail="series=${series:-?} dim0=${dims}"
  result="OK"
  if [[ "$PIX" == 1 ]]; then
    if bfconvert -overwrite -series 0 -crop 0,0,256,256 "$target" "$DEST/$name.crop.png" >"$DEST/$name.bfconvert.log" 2>&1; then
      detail="$detail pixels=OK"
    else
      detail="$detail pixels=FAIL"; result="PIXFAIL"; fail=$((fail+1))
    fi
  fi
  [[ "$result" == "OK" ]] && pass=$((pass+1))
  printf "%-28s  %-8s  %s\n" "$name" "$result" "$detail"
done

echo
echo "Bio-Formats: $pass OK, $fail FAIL, $na N/A.  showinf logs in $DEST"
[[ "$fail" -gt 0 ]] && exit 1 || exit 0
