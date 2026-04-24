import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

import yaml


SCRIPTS_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPTS_DIR.parents[2]
SCRIPT_PATH = SCRIPTS_DIR / "sync_hive_contract.py"
MATRIX_PATH = REPO_ROOT / "packages/openai-contract/matrix/support-matrix.json"
UPSTREAM_SPEC_PATH = REPO_ROOT / "packages/openai-contract/upstream/openapi.yaml"


def load_module():
    if not SCRIPT_PATH.exists():
        raise AssertionError(f"Missing generator script: {SCRIPT_PATH}")

    spec = importlib.util.spec_from_file_location("sync_hive_contract", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise AssertionError(f"Unable to load script module from {SCRIPT_PATH}")

    module = importlib.util.module_from_spec(spec)
    sys.modules.setdefault("sync_hive_contract", module)
    spec.loader.exec_module(module)
    return module


class SyncHiveContractTests(unittest.TestCase):
    def test_exports_required_helpers(self):
        module = load_module()

        self.assertTrue(hasattr(module, "load_matrix"))
        self.assertTrue(hasattr(module, "render_openapi"))
        self.assertTrue(hasattr(module, "render_markdown"))

    def test_render_openapi_generates_hive_specific_contract(self):
        module = load_module()
        matrix_document, matrix_lookup = module.load_matrix(MATRIX_PATH)

        self.assertIn("GET /v1/models", matrix_lookup)
        self.assertEqual("supported_now", matrix_lookup["GET /v1/models"]["status"])
        self.assertEqual(1, matrix_lookup["GET /v1/models"]["phase"])

        with tempfile.TemporaryDirectory() as tmpdir:
            output_path = Path(tmpdir) / "hive-openapi.yaml"
            public_operations = module.render_openapi(
                UPSTREAM_SPEC_PATH,
                matrix_lookup,
                output_path,
            )

            self.assertGreater(public_operations, 0)

            rendered_text = output_path.read_text(encoding="utf-8")
            rendered_spec = yaml.safe_load(rendered_text)

            self.assertEqual(
                [
                    {
                        "url": "/v1",
                        "description": "Hive API base URL on the current host",
                    }
                ],
                rendered_spec["servers"],
            )
            self.assertEqual(
                "supported_now",
                rendered_spec["paths"]["/models"]["get"]["x-hive-status"],
            )
            self.assertEqual(
                1,
                rendered_spec["paths"]["/models"]["get"]["x-hive-phase"],
            )
            self.assertNotIn("https://api.openai.com/v1", rendered_text)
            self.assertNotIn("/organization/admin_api_keys", rendered_spec["paths"])

    def test_render_markdown_uses_matrix_provenance_and_section_order(self):
        module = load_module()
        matrix_document, _ = module.load_matrix(MATRIX_PATH)

        with tempfile.TemporaryDirectory() as tmpdir:
            output_path = Path(tmpdir) / "support-matrix.md"
            module.render_markdown(matrix_document, output_path)

            rendered = output_path.read_text(encoding="utf-8")

            self.assertIn(
                "Generated from `packages/openai-contract/matrix/support-matrix.json`. "
                "Do not edit manually.",
                rendered,
            )
            self.assertIn("| Method | Path | Status | Phase | Notes |", rendered)

            supported_now = rendered.index("## supported_now")
            planned_for_launch = rendered.index("## planned_for_launch")
            unsupported = rendered.index("## explicitly_unsupported_at_launch")
            out_of_scope = rendered.index("## out_of_scope")

            self.assertLess(supported_now, planned_for_launch)
            self.assertLess(planned_for_launch, unsupported)
            self.assertLess(unsupported, out_of_scope)


if __name__ == "__main__":
    unittest.main()
