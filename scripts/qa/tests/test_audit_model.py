from audit_model import Case, Finding, SEVERITIES


def test_case_roundtrips_through_dict():
    c = Case(
        id="svs-to-tiff",
        cmd_argv=["convert", "--to", "tiff", "-o", "out.tiff", "in.svs"],
        input="in.svs",
        input_format="svs",
        output="out.tiff",
        output_container="tiff",
        transform_type="container-swap",
        requested_codec=None,
        factor=1,
        rect=None,
        lossless=False,
        expect_error=False,
        source_props={"mpp": 0.5},
    )
    d = c.to_dict()
    assert Case.from_dict(d) == c


def test_finding_severity_must_be_known():
    f = Finding(
        case_id="x", family="codec", invariant="output-codec-honored",
        severity="silent-wrong-output", expected="htj2k", actual="jpeg",
        repro="wsitools convert --to cog-wsi --codec htj2k -o o in",
    )
    assert f.severity in SEVERITIES
    assert f.to_dict()["family"] == "codec"
