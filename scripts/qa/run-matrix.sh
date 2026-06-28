#!/usr/bin/env bash
#
# run-matrix.sh — exercise a broad matrix of wsitools functions and write the
# resulting artifacts (plus a manifest) into an output directory for manual
# inspection in viewers (OpenSlide, QuPath, ImageScope, Hamamatsu, Bio-Formats).
#
# This is NOT a programmatic test. It just *generates* outputs and a manifest;
# you then eyeball them (see MANUAL-TEST-PLAN.md) and/or run the auto-validators
# (check-openslide.sh, check-bioformats.sh).
#
# Usage:
#   scripts/qa/run-matrix.sh [--big] [--clean]
#
# Env overrides:
#   WSITOOLS=/path/to/wsitools   use a prebuilt binary (else built from this repo)
#   SRC=/path/to/sample_files    source fixtures (default: ./sample_files)
#   OUT=/path/to/outdir          output dir   (default: /tmp/wsitools-qa)
#
# Flags:
#   --big     also run cases driven by large sources (NDPI, IFE/Iris) — slow, GBs
#   --clean   remove OUT before running
#
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC="${SRC:-$ROOT/sample_files}"
OUT="${OUT:-/tmp/wsitools-qa}"
BIG=0
CLEAN=0
for a in "$@"; do
  case "$a" in
    --big) BIG=1 ;;
    --clean) CLEAN=1 ;;
    *) echo "unknown flag: $a" >&2; exit 2 ;;
  esac
done

# ---- resolve the wsitools binary -------------------------------------------
if [[ -n "${WSITOOLS:-}" ]]; then
  BIN="$WSITOOLS"
else
  BIN="$OUT/_bin/wsitools"
  mkdir -p "$OUT/_bin"
  echo ">> building wsitools from $ROOT ..."
  ( cd "$ROOT" && go build -o "$BIN" ./cmd/wsitools ) || { echo "build failed"; exit 1; }
fi
echo ">> wsitools: $BIN ($($BIN version 2>/dev/null | head -1))"
echo ">> sources:  $SRC"
echo ">> output:   $OUT"

[[ "$CLEAN" == 1 ]] && rm -rf "$OUT"/cases "$OUT"/manifest.tsv "$OUT"/logs
mkdir -p "$OUT/cases" "$OUT/logs" "$OUT/_assets"
MANIFEST="$OUT/manifest.tsv"
printf "id\tcategory\tdescription\tsource\toutput\tstatus\n" > "$MANIFEST"

# ---- helpers ----------------------------------------------------------------
# run ID CATEGORY "DESC" SRCFILE OUTPATH -- cmd...
run() {
  local id="$1" cat="$2" desc="$3" srcf="$4" outp="$5"; shift 5
  [[ "$1" == "--" ]] && shift
  if [[ -n "$srcf" && ! -e "$srcf" ]]; then
    printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$id" "$cat" "$desc" "${srcf#$SRC/}" "${outp#$OUT/}" "SKIP(no-src)" >> "$MANIFEST"
    printf "  [%s] SKIP (missing %s)\n" "$id" "${srcf#$SRC/}"
    return
  fi
  local log="$OUT/logs/$id.log"
  if "$@" >"$log" 2>&1; then
    printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$id" "$cat" "$desc" "${srcf#$SRC/}" "${outp#$OUT/}" "OK" >> "$MANIFEST"
    printf "  [%s] OK   %s\n" "$id" "$desc"
  else
    printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$id" "$cat" "$desc" "${srcf#$SRC/}" "${outp#$OUT/}" "FAIL" >> "$MANIFEST"
    printf "  [%s] FAIL %s  (see logs/%s.log)\n" "$id" "$desc" "$id"
  fi
}

C="$OUT/cases"

# ---- pick representative sources (smallest per format) ----------------------
SVS_SMALL="$SRC/svs/CMU-1-Small-Region.svs"     # 1-level jpeg + thumbnail/label/overview
SVS_MULTI="$SRC/svs/CMU-1.svs"                   # 3-level jpeg, full associated
SVS_JP2K="$SRC/svs/JP2K-33003-1.svs"             # 3-level jpeg2000
BIF="$SRC/bif/S12-18199-1A.bif"                  # Ventana DP200 (stitched)
OME="$SRC/ome-tiff/CMU-1-Small-Region.ome.tiff"
COG="$SRC/cog-wsi/CMU-1-Small-Region_cog-wsi.tiff"
DICOM="$SRC/dicom/scan_621_grundium_dicom"
NDPI="$SRC/ndpi/CMU-1.ndpi"                      # big
IFE="$SRC/ife/425248_JPEG.iris"                  # big

echo; echo "== A. read-side inspection (sanity) =="
for f in "$SVS_SMALL" "$BIF" "$OME" "$COG" "$DICOM" "$SVS_JP2K"; do
  [[ -e "$f" ]] || continue
  b="$(basename "$f")"
  run "info-$b"     read "info $b"        "$f" "$OUT/logs/info-$b.log"     -- "$BIN" info "$f"
  run "validate-$b" read "validate $b"    "$f" "$OUT/logs/validate-$b.log" -- "$BIN" validate "$f"
done
[[ "$BIG" == 1 ]] && for f in "$NDPI" "$IFE"; do [[ -e "$f" ]] && run "info-$(basename "$f")" read "info $(basename "$f")" "$f" "$OUT/logs/info-$(basename "$f").log" -- "$BIN" info "$f"; done

echo; echo "== B. container conversions (from a small SVS) =="
run b-svs      container "SVS -> svs"      "$SVS_SMALL" "$C/b_svs.svs"           -- "$BIN" convert --to svs      -f -o "$C/b_svs.svs"          "$SVS_SMALL"
run b-tiff     container "SVS -> tiff"     "$SVS_SMALL" "$C/b_tiff.tiff"         -- "$BIN" convert --to tiff     -f -o "$C/b_tiff.tiff"        "$SVS_SMALL"
run b-ome      container "SVS -> ome-tiff" "$SVS_SMALL" "$C/b_ome.ome.tiff"      -- "$BIN" convert --to ome-tiff -f -o "$C/b_ome.ome.tiff"     "$SVS_SMALL"
run b-cog      container "SVS -> cog-wsi"  "$SVS_SMALL" "$C/b_cog.tiff"          -- "$BIN" convert --to cog-wsi  -f -o "$C/b_cog.tiff"         "$SVS_SMALL"
run b-dzi      container "SVS -> dzi"      "$SVS_SMALL" "$C/b_dzi.dzi"           -- "$BIN" convert --to dzi      -f -o "$C/b_dzi.dzi"          "$SVS_SMALL"
run b-szi      container "SVS -> szi"      "$SVS_SMALL" "$C/b_szi.szi"           -- "$BIN" convert --to szi      -f -o "$C/b_szi.szi"          "$SVS_SMALL"
run b-dicom    container "SVS -> dicom"    "$SVS_SMALL" "$C/b_dicom"             -- "$BIN" convert --to dicom    -f -o "$C/b_dicom"            "$SVS_SMALL"
run b-bif      container "SVS -> bif"      "$SVS_SMALL" "$C/b_bif.bif"           -- "$BIN" convert --to bif      -f -o "$C/b_bif.bif"          "$SVS_SMALL"
run b-ife      container "SVS -> ife(256)" "$SVS_SMALL" "$C/b_ife.iris"          -- "$BIN" convert --to ife --tile-size 256 -f -o "$C/b_ife.iris" "$SVS_SMALL"

echo; echo "== C. codec coverage (-> tiff) =="
# jpeg (default) + jpeg2000 are standard-TIFF-conformant (ImageScope/Bio-Formats
# read them). jpegxl/avif/webp/htj2k have no standard TIFF compression tag, so
# they need --allow-nonconformant and are readable ONLY by wsitools/opentile —
# validate those with `wsitools info/validate`, not external viewers.
run c-jpeg2000 codec "SVS -> tiff --codec jpeg2000" "$SVS_SMALL" "$C/c_jpeg2000.tiff" -- "$BIN" convert --to tiff --codec jpeg2000 -f -o "$C/c_jpeg2000.tiff" "$SVS_SMALL"
for codec in jpegxl avif webp htj2k; do
  run "c-$codec" codec-novel "SVS -> tiff --codec $codec (wsitools-only)" "$SVS_SMALL" "$C/c_$codec.tiff" -- "$BIN" convert --to tiff --codec "$codec" --allow-nonconformant -f -o "$C/c_$codec.tiff" "$SVS_SMALL"
done

echo; echo "== D. transforms (factor / crop / tile-size / downsample / crop / transcode) =="
run d-factor2   transform "SVS --factor 2 -> svs"          "$SVS_MULTI" "$C/d_factor2.svs"   -- "$BIN" convert --to svs --factor 2 -f -o "$C/d_factor2.svs" "$SVS_MULTI"
run d-factor4   transform "SVS --factor 4 -> tiff"         "$SVS_MULTI" "$C/d_factor4.tiff"  -- "$BIN" convert --to tiff --factor 4 -f -o "$C/d_factor4.tiff" "$SVS_MULTI"
run d-rect      transform "SVS --rect crop -> svs"         "$SVS_MULTI" "$C/d_rect.svs"      -- "$BIN" convert --to svs --rect 0,0,4096,4096 -f -o "$C/d_rect.svs" "$SVS_MULTI"
run d-tilesize  transform "SVS --tile-size 512 -> svs"     "$SVS_MULTI" "$C/d_tile512.svs"   -- "$BIN" convert --to svs --codec jpeg --tile-size 512 -f -o "$C/d_tile512.svs" "$SVS_MULTI"
run d-downs     transform "downsample --factor 2 (svs)"    "$SVS_MULTI" "$C/d_downsample.svs" -- "$BIN" downsample --factor 2 -f -o "$C/d_downsample.svs" "$SVS_MULTI"
run d-crop      transform "crop (lossy) svs"               "$SVS_MULTI" "$C/d_crop.svs"      -- "$BIN" crop --rect 1000,1000,6000,6000 -f -o "$C/d_crop.svs" "$SVS_MULTI"
run d-cropll    transform "crop --lossless svs"            "$SVS_MULTI" "$C/d_crop_lossless.svs" -- "$BIN" crop --rect 1000,1000,6000,6000 --lossless -f -o "$C/d_crop_lossless.svs" "$SVS_MULTI"
run d-trans     transform "transcode jpeg->jpeg2000 (svs)" "$SVS_MULTI" "$C/d_transcode.svs" -- "$BIN" transcode --codec jpeg2000 -f -o "$C/d_transcode.svs" "$SVS_MULTI"

echo; echo "== E. cross-format -> SVS (the ImageScope-critical set) =="
run e-bif    crossfmt "BIF -> svs"       "$BIF"  "$C/e_bif2svs.svs"  -- "$BIN" convert --to svs -f -o "$C/e_bif2svs.svs"  "$BIF"
run e-ome    crossfmt "OME-TIFF -> svs"  "$OME"  "$C/e_ome2svs.svs"  -- "$BIN" convert --to svs -f -o "$C/e_ome2svs.svs"  "$OME"
run e-cog    crossfmt "COG-WSI -> svs"   "$COG"  "$C/e_cog2svs.svs"  -- "$BIN" convert --to svs -f -o "$C/e_cog2svs.svs"  "$COG"
run e-dicom  crossfmt "DICOM -> svs"     "$DICOM" "$C/e_dicom2svs.svs" -- "$BIN" convert --to svs -f -o "$C/e_dicom2svs.svs" "$DICOM"
if [[ "$BIG" == 1 ]]; then
  run e-ndpi crossfmt "NDPI -> svs"      "$NDPI" "$C/e_ndpi2svs.svs" -- "$BIN" convert --to svs -f -o "$C/e_ndpi2svs.svs" "$NDPI"
  run e-ife  crossfmt "IFE/Iris -> svs"  "$IFE"  "$C/e_ife2svs.svs"  -- "$BIN" convert --to svs -f -o "$C/e_ife2svs.svs"  "$IFE"
fi

echo; echo "== F. -> DICOM transforms =="
run f-dcm-factor crossfmt "SVS --factor 2 -> dicom" "$SVS_MULTI" "$C/f_svs2dicom_f2" -- "$BIN" convert --to dicom --factor 2 -f -o "$C/f_svs2dicom_f2" "$SVS_MULTI"
run f-dcm-dcm    crossfmt "DICOM --factor 2 -> dicom" "$DICOM" "$C/f_dicom2dicom_f2" -- "$BIN" convert --to dicom --factor 2 -f -o "$C/f_dicom2dicom_f2" "$DICOM"

echo; echo "== G. associated-image editing + extraction =="
# Produce a replacement image from a source that has a label.
if [[ -e "$SVS_MULTI" ]]; then
  "$BIN" extract --type label --format png -o "$OUT/_assets/label.png" "$SVS_MULTI" >/dev/null 2>&1 || true
fi
REPL="$OUT/_assets/label.png"
run g-lbl-rm   associated "label remove (svs)"   "$SVS_MULTI" "$C/g_label_removed.svs"   -- "$BIN" label remove   -o "$C/g_label_removed.svs"   --overwrite "$SVS_MULTI"
if [[ -e "$REPL" ]]; then
  run g-lbl-rep associated "label replace (svs)"  "$SVS_MULTI" "$C/g_label_replaced.svs"  -- "$BIN" label replace  --image "$REPL" -o "$C/g_label_replaced.svs"  --overwrite "$SVS_MULTI"
  run g-mac-rep associated "macro replace (svs)"  "$SVS_MULTI" "$C/g_macro_replaced.svs"  -- "$BIN" macro replace  --image "$REPL" -o "$C/g_macro_replaced.svs"  --overwrite "$SVS_MULTI"
fi
run g-ovr-rm   associated "overview remove (svs)" "$SVS_MULTI" "$C/g_overview_removed.svs" -- "$BIN" overview remove -o "$C/g_overview_removed.svs" --overwrite "$SVS_MULTI"
# Extraction of each associated type the source actually carries (CMU-1 has
# thumbnail/label/overview, no macro).
for t in label overview thumbnail; do
  run "g-ext-$t" associated "extract $t -> png" "$SVS_MULTI" "$OUT/_assets/extracted_$t.png" -- "$BIN" extract --type "$t" --format png -o "$OUT/_assets/extracted_$t.png" "$SVS_MULTI"
done

echo
echo "==========================================================================="
awk -F'\t' 'NR>1{c[$6]++} END{for(k in c) printf "  %-12s %d\n", k, c[k]}' "$MANIFEST"
echo "  manifest: $MANIFEST"
echo "  next: scripts/qa/check-openslide.sh   (auto-validate openslide-readable outputs)"
echo "        scripts/qa/check-bioformats.sh  (auto-validate via Bio-Formats showinf)"
echo "        scripts/qa/MANUAL-TEST-PLAN.md  (what to open where + what to look for)"
echo "==========================================================================="
