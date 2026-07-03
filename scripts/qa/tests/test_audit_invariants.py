from audit_model import Case
import audit_invariants as inv


def _case(**kw):
    base = dict(id="c", cmd_argv=["convert"], input="in", input_format="svs",
                output="out", output_container="tiff", transform_type="container-swap",
                requested_codec=None, factor=1, rect=None, lossless=False,
                expect_error=False, source_props={})
    base.update(kw)
    return Case(**base)


def _info(levels, **md):
    return {"format": "tiff", "metadata": md, "levels": levels, "associated_images": []}


def _lvl(w, h, codec="jpeg", sub="4:4:4"):
    return {"index": 0, "width": w, "height": h, "tile_width": 256, "tile_height": 256,
            "compression": codec, "quality": {"codec": codec.upper(), "lossless": False,
            "quality_estimate": 85, "chroma_subsampling": sub, "notes": ""}}


def test_container_swap_preserves_l0_dims():
    src = _info([_lvl(2000, 3000)])
    ok = _info([_lvl(2000, 3000)])
    bad = _info([_lvl(1000, 3000)])
    assert inv.check_geometry(_case(), src, ok) == []
    f = inv.check_geometry(_case(), src, bad)
    assert len(f) == 1 and f[0].family == "geometry"


def test_factor_scales_l0_dims_within_tolerance():
    src = _info([_lvl(4000, 4000)])
    out = _info([_lvl(1000, 1000)])  # factor 4
    assert inv.check_geometry(_case(transform_type="factor", factor=4), src, out) == []
    bad = _info([_lvl(2000, 2000)])  # only halved
    assert len(inv.check_geometry(_case(transform_type="factor", factor=4), src, bad)) == 1


def test_codec_must_match_requested():
    src = _info([_lvl(100, 100, codec="jpeg")])
    out = _info([_lvl(100, 100, codec="jpeg")])
    c = _case(requested_codec="htj2k")
    f = inv.check_codec(c, src, out)
    assert len(f) == 1 and f[0].severity == "silent-wrong-output"


def test_codec_uniform_across_levels():
    src = _info([_lvl(100, 100)])
    out = {"format": "tiff", "metadata": {}, "associated_images": [],
           "levels": [_lvl(100, 100, codec="jpeg"), _lvl(50, 50, codec="jpeg2000")]}
    f = inv.check_codec(_case(), src, out)
    assert any(x.invariant == "codec-uniform" for x in f)


def test_pyramid_dims_strictly_decreasing():
    out = {"format": "tiff", "metadata": {}, "associated_images": [],
           "levels": [_lvl(4000, 4000), _lvl(2000, 2000), _lvl(2000, 2000)]}
    f = inv.check_pyramid(_case(), out)
    assert any(x.invariant == "levels-monotonic" for x in f)


def test_subsampling_tag_must_match_bytes():
    # Uniform bytes (both 4:2:0) so ONLY the tag-vs-bytes rule can fire; L1's tag
    # lies (claims 4:4:4 while the SOF bytes are 4:2:0) — the lossless-crop bug shape.
    out = {"format": "svs", "metadata": {}, "associated_images": [],
           "levels": [_lvl(4000, 4000, sub="4:2:0"), _lvl(2000, 2000, sub="4:2:0")]}
    out_subtags = ["4:2:0", "4:4:4"]
    f = inv.check_subsampling(_case(), out, out_subtags)
    assert len(f) == 1 and f[0].invariant == "subsampling-tag-matches-bytes"
    assert f[0].severity == "conformance"


def test_subsampling_consistent_across_pyramid():
    out = {"format": "svs", "metadata": {}, "associated_images": [],
           "levels": [_lvl(4000, 4000, sub="4:4:4"), _lvl(2000, 2000, sub="4:2:0")]}
    out_subtags = ["4:4:4", "4:2:0"]
    f = inv.check_subsampling(_case(), out, out_subtags)
    assert any(x.invariant == "subsampling-uniform" for x in f)


def test_metadata_sanity_flags_negative_mpp():
    # mpp == 0 means "unknown" (benign; metadata LOSS is a consistency check, not
    # sanity). A NEGATIVE mpp is genuinely invalid and must be flagged.
    out = _info([_lvl(100, 100)], mpp=-0.5, mpp_x=-0.5, mpp_y=-0.5, magnification=20)
    f = inv.check_metadata_sanity(_case(), out)
    assert any(x.invariant == "mpp-positive" for x in f)
    # mpp == 0 (unknown) must NOT be flagged as insane.
    clean = _info([_lvl(100, 100)], mpp=0, mpp_x=0, mpp_y=0, magnification=20)
    assert not any(x.invariant == "mpp-positive" for x in inv.check_metadata_sanity(_case(), clean))


def test_metadata_sanity_flags_anisotropic_mpp_disagreeing_with_mpp():
    out = _info([_lvl(100, 100)], mpp=0.5, mpp_x=0.5, mpp_y=0.9, magnification=20)
    f = inv.check_metadata_sanity(_case(), out)
    assert any(x.invariant == "mpp-axes-consistent" for x in f)


def test_metadata_sanity_flags_implausible_magnification():
    out = _info([_lvl(100, 100)], mpp=0.5, mpp_x=0.5, mpp_y=0.5, magnification=9000)
    f = inv.check_metadata_sanity(_case(), out)
    assert any(x.invariant == "magnification-plausible" for x in f)


def test_metadata_consistency_info_vs_dumpifds_dims():
    out = _info([_lvl(2000, 3000)])
    f_ok = inv.check_metadata_consistency(_case(), out, [(2000, 3000)])
    assert f_ok == []
    f_bad = inv.check_metadata_consistency(_case(), out, [(2000, 9999)])
    assert any(x.invariant == "info-matches-dumpifds-dims" for x in f_bad)


def test_cross_container_flags_scale_metadata_loss_vs_source():
    src = _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20)
    per_container = {
        "svs": _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20),
        "ome-tiff": _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=0),  # mag dropped
    }
    f = inv.check_cross_container("cmu2", src, per_container, "repro")
    assert any(x.invariant == "scale-metadata-preserved" and "magnification" in str(x.expected) for x in f)
    # bif's synthesized identity is NOT flagged (identity checked only for TIFF family).
    per2 = {"bif": _info([_lvl(2400, 3120)], make="Roche", mpp=0.5, magnification=20)}
    f2 = inv.check_cross_container("cmu2", src, per2, "repro")
    assert not any(x.invariant == "identity-metadata-preserved" for x in f2)
    # bif's padded dims are NOT flagged either.
    assert not any(x.invariant == "cross-container-l0-dims" for x in f2)


def test_cross_container_agreement_is_clean():
    src = _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20)
    per = {c: _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20) for c in ("svs", "tiff")}
    assert inv.check_cross_container("cmu2", src, per, "repro") == []
