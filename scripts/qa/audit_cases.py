"""Enumerate the tiered representative audit matrix as a list[Case]. Only emits a
case when its input fixture exists on disk (missing fixtures are coverage gaps,
logged by the orchestrator, not errors)."""
from __future__ import annotations

import os
from audit_model import Case

# Output containers reachable via `convert --to` for the TIFF-family sources.
CONTAINERS = ["svs", "tiff", "ome-tiff", "cog-wsi", "dzi", "szi", "dicom", "bif", "ife"]
EXT = {"svs": "svs", "tiff": "tiff", "ome-tiff": "ome.tiff", "cog-wsi": "cog.tiff",
       "dzi": "dzi", "szi": "szi", "dicom": "dcmdir", "bif": "bif", "ife": "iris"}
# Codecs to sweep on the deep-transform sources (jpegxl needs --allow-nonconformant).
CODECS = ["jpeg", "jpeg2000", "htj2k", "avif", "webp", "jpegxl"]


def _fx(fixtures: str, rel: str) -> str | None:
    p = os.path.join(fixtures, rel)
    return p if os.path.exists(p) else None


def enumerate_cases(fixtures: str, big: bool) -> list[Case]:
    cases: list[Case] = []

    def add(cid, argv, inp, infmt, out, container, tt, **kw):
        cases.append(Case(id=cid, cmd_argv=argv, input=inp, input_format=infmt, output=out,
                          output_container=container, transform_type=tt,
                          requested_codec=kw.get("codec"), factor=kw.get("factor", 1),
                          rect=kw.get("rect"), lossless=kw.get("lossless", False),
                          expect_error=kw.get("expect_error", False), source_props={}))

    small = _fx(fixtures, "svs/CMU-1-Small-Region.svs")
    s444 = _fx(fixtures, "svs/CMU-2.svs")
    s420 = _fx(fixtures, "svs/scan_620_.svs")
    sjp2k = _fx(fixtures, "svs/JP2K-33003-1.svs")

    # --- T1: broad-shallow read commands over every present input format ---
    read_inputs = {
        "svs": small, "generic-tiff": _fx(fixtures, "generic-tiff/CMU-1.tiff"),
        "ome-tiff": _fx(fixtures, "ome-tiff/CMU-1.ome.tiff"), "cog-wsi": _fx(fixtures, "cog-wsi/CMU-1_cog-wsi.tiff"),
        "dicom": _fx(fixtures, "dicom/Leica-4"), "ndpi": _fx(fixtures, "ndpi/CMU-1.ndpi") if big else None,
        "bif": _fx(fixtures, "bif/OS-2.bif"), "ife": _fx(fixtures, "ife/425248_JPEG.iris") if big else None,
    }
    for fmt, path in read_inputs.items():
        if not path:
            continue
        add(f"info-{fmt}", ["info", "--json", path], path, fmt, path, "read", "read")
        add(f"dump-{fmt}", ["dump-ifds", "--json", path], path, fmt, path, "read", "read")
        add(f"validate-{fmt}", ["validate", "--json", path], path, fmt, path, "read", "read")
        add(f"hash-{fmt}", ["hash", "--mode", "pixel", path], path, fmt, path, "read", "read")

    # --- T1: container-swap — small SVS -> every container (defaults) ---
    if small:
        for c in CONTAINERS:
            out = f"OUTDIR/swap_{c}.{EXT[c]}"
            add(f"swap-svs-{c}", ["convert", "--to", c, "-f", "-o", out, small],
                small, "svs", out, c, "container-swap")

    # --- T2: deep transforms on the representative multi-level sources ---
    for role, src in (("444", s444), ("420", s420), ("jp2k", sjp2k)):
        if not src:
            continue
        for codec in CODECS:
            out = f"OUTDIR/codec_{role}_{codec}.tiff"
            argv = ["convert", "--to", "tiff", "--codec", codec]
            if codec == "jpegxl":
                argv.append("--allow-nonconformant")
            argv += ["-f", "-o", out, src]
            add(f"codec-{role}-{codec}", argv, src, "svs", out, "tiff", "recodec", codec=codec)
        for n in (2, 4, 8):
            out = f"OUTDIR/factor_{role}_{n}.svs"
            add(f"factor-{role}-{n}", ["convert", "--to", "svs", "--factor", str(n), "-f", "-o", out, src],
                src, "svs", out, "svs", "factor", factor=n)
            outd = f"OUTDIR/down_{role}_{n}.svs"
            add(f"down-{role}-{n}", ["downsample", "--factor", str(n), "-f", "-o", outd, src],
                src, "svs", outd, "svs", "downsample", factor=n)
        rect = "4000,4000,8192,8192"
        add(f"crop-{role}", ["crop", "--x", "4000", "--y", "4000", "--w", "8192", "--h", "8192",
            "-f", "-o", f"OUTDIR/crop_{role}.svs", src], src, "svs", f"OUTDIR/crop_{role}.svs", "svs", "crop", rect=rect)
        add(f"cropll-{role}", ["crop", "--lossless", "--x", "4000", "--y", "4000", "--w", "8192", "--h", "8192",
            "-f", "-o", f"OUTDIR/cropll_{role}.svs", src], src, "svs", f"OUTDIR/cropll_{role}.svs",
            "svs", "crop", rect=rect, lossless=True)
        add(f"tilesize-{role}", ["convert", "--to", "tiff", "--codec", "jpeg", "--tile-size", "512",
            "-f", "-o", f"OUTDIR/tile_{role}.tiff", src], src, "svs", f"OUTDIR/tile_{role}.tiff", "tiff", "recodec", codec="jpeg")

    # --- T3: cross-format -> svs ---
    for fmt, path in (("bif", read_inputs["bif"]), ("ome-tiff", read_inputs["ome-tiff"]),
                      ("cog-wsi", read_inputs["cog-wsi"]), ("dicom", read_inputs["dicom"])):
        if path:
            out = f"OUTDIR/x_{fmt}_to_svs.svs"
            add(f"x-{fmt}-svs", ["convert", "--to", "svs", "-f", "-o", out, path],
                path, fmt, out, "svs", "container-swap")

    return cases
