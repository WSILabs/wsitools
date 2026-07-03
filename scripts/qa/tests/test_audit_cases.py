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
