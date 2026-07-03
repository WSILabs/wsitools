import os
import audit_cases


def test_enumerate_only_includes_existing_inputs(tmp_path):
    (tmp_path / "svs").mkdir()
    (tmp_path / "svs" / "CMU-1-Small-Region.svs").write_bytes(b"x")
    cases = audit_cases.enumerate_cases(str(tmp_path), big=False)
    assert cases, "expected some cases for a present fixture"
    for c in cases:
        assert os.path.exists(c.input), f"case {c.id} references missing input {c.input}"


def test_container_swap_cases_have_expected_transform_type(tmp_path):
    (tmp_path / "svs").mkdir()
    (tmp_path / "svs" / "CMU-1-Small-Region.svs").write_bytes(b"x")
    cases = audit_cases.enumerate_cases(str(tmp_path), big=False)
    swaps = [c for c in cases if c.transform_type == "container-swap"]
    assert any(c.output_container == "tiff" for c in swaps)


def test_deep_transform_cases_generate_when_sources_present(tmp_path):
    # Create the T2 source fixtures so the deep-transform add() calls execute.
    svs = tmp_path / "svs"
    svs.mkdir()
    for name in ("CMU-1-Small-Region.svs", "CMU-2.svs", "scan_620_.svs", "JP2K-33003-1.svs"):
        (svs / name).write_bytes(b"x")
    cases = audit_cases.enumerate_cases(str(tmp_path), big=False)
    tts = {c.transform_type for c in cases}
    assert {"factor", "downsample", "crop", "recodec"} <= tts
    # every case has all required fields populated (guards the add() arity bug)
    for c in cases:
        assert c.transform_type and c.output_container
    # lossless crop cases are marked lossless
    assert any(c.transform_type == "crop" and c.lossless for c in cases)
