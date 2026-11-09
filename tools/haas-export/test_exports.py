"""Archive extraction checks that need no model or export dependencies."""

import io
import tarfile
import tempfile
import unittest
from pathlib import Path

from export_onnx import read_tokenizer


class TokenizerExtractionTests(unittest.TestCase):
    def archive(self, root, names):
        path = root / "model.nemo"
        with tarfile.open(path, "w") as archive:
            for name in names:
                info = tarfile.TarInfo(name)
                info.size = len(b"tokenizer bytes")
                archive.addfile(info, io.BytesIO(b"tokenizer bytes"))
        return path

    def test_read_without_extracting_archive_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = self.archive(root, ["../outside_tokenizer.model"])
            self.assertEqual(read_tokenizer(path), b"tokenizer bytes")
            self.assertEqual(list(root.iterdir()), [path])

    def test_missing_or_ambiguous_tokenizer_is_rejected(self):
        for names in ([], ["a_tokenizer.model", "b_tokenizer.model"]):
            with self.subTest(names=names), tempfile.TemporaryDirectory() as tmp:
                path = self.archive(Path(tmp), names)
                with self.assertRaisesRegex(ValueError, "exactly one"):
                    read_tokenizer(path)


if __name__ == "__main__":
    unittest.main()
