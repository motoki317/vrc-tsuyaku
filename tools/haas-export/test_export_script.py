import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


class ExportScriptTests(unittest.TestCase):
    def run_export(self, bundle, revision):
        script = Path(__file__).resolve().with_name("export.sh")
        bash = shutil.which("bash")
        self.assertIsNotNone(bash)
        return subprocess.run(
            [bash, str(script)],
            cwd=script.parent,
            env={
                "HAAS_NEMO": str(bundle / "unused.nemo"),
                "HAAS_CHECKPOINT_REVISION": revision,
                "HAAS_BUNDLE": str(bundle),
                "PATH": str(bundle),
            },
            capture_output=True,
            text=True,
            timeout=10,
        )

    def test_rejects_bundle_with_only_a_dot_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            bundle = Path(tmp)
            (bundle / ".keep").touch()
            result = self.run_export(bundle, "a" * 40)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(result.stderr, "HAAS_BUNDLE must be empty before export.\n")

    def test_rejects_invalid_checkpoint_revision(self):
        for revision in ("the-hugging-face-commit-of-that-checkpoint", "A" * 40):
            with self.subTest(revision=revision), tempfile.TemporaryDirectory() as tmp:
                result = self.run_export(Path(tmp), revision)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertEqual(
                    result.stderr,
                    "Set HAAS_CHECKPOINT_REVISION to the full Hugging Face checkpoint commit "
                    "(40 lowercase hex characters).\n",
                )
