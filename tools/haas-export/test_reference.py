"""Reference generation checks with synthetic WAVs and a stub recognizer."""

from contextlib import contextmanager
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import wave

import numpy as np

from write_reference import read_manifests, read_wav, write_reference


class ReferenceTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name)
        self.output = self.directory / "reference.jsonl"

    def manifest(self, name, rows):
        path = self.directory / name
        path.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")
        return path

    def wav(self, name, *, channels=1, rate=16000, width=2):
        path = self.directory / f"{name}.wav"
        with wave.open(str(path), "wb") as destination:
            destination.setnchannels(channels)
            destination.setsampwidth(width)
            destination.setframerate(rate)
            destination.writeframes(np.array([-32768, 0, 16384, 32767], dtype="<i2").tobytes())
        return path

    def test_multiple_manifests_preserve_order_text_and_pcm_values(self):
        first = self.manifest("first.jsonl", [{"id": "b", "text": "猫"}])
        second = self.manifest("second.jsonl", [{"id": "a", "text": "犬"}])
        for clip in ("a", "b"):
            self.wav(clip)
        with patch("write_reference.HaasFullOnnx") as constructor:
            constructor.return_value.transcribe.side_effect = [("猫。", [], []), ("", [], [])]
            write_reference(self.directory, "encoder.int8.onnx", [first, second], self.directory, self.output)
        constructor.assert_called_once_with(self.directory, threads=4, encoder_file="encoder.int8.onnx")
        audio = constructor.return_value.transcribe.call_args_list[0].args[0]
        self.assertEqual(audio.dtype, np.float32)
        np.testing.assert_array_equal(audio, [-1, 0, 0.5, 32767 / 32768])
        self.assertEqual([json.loads(line) for line in self.output.read_text(encoding="utf-8").splitlines()], [
            {"id": "b", "hyp": "猫。", "ref": "猫"},
            {"id": "a", "hyp": "", "ref": "犬"},
        ])

    def test_refuses_overwrite_before_loading_model(self):
        manifest = self.manifest("manifest.jsonl", [{"id": "a", "text": "text"}])
        self.output.write_bytes(b"keep")
        with patch("write_reference.HaasFullOnnx") as constructor:
            with self.assertRaises(FileExistsError):
                write_reference(self.directory, "encoder.onnx", [manifest], self.directory, self.output)
            constructor.assert_not_called()
        self.assertEqual(self.output.read_bytes(), b"keep")

    def test_failed_inference_removes_partial_reference(self):
        manifest = self.manifest("manifest.jsonl", [{"id": "a", "text": "a"}, {"id": "b", "text": "b"}])
        for clip in ("a", "b"):
            self.wav(clip)
        with patch("write_reference.HaasFullOnnx") as constructor:
            constructor.return_value.transcribe.side_effect = [("a", [], []), RuntimeError("inference failed")]
            with self.assertRaisesRegex(RuntimeError, "inference failed"):
                write_reference(self.directory, "encoder.onnx", [manifest], self.directory, self.output)
        self.assertFalse(self.output.exists())

    def test_rejects_empty_invalid_and_duplicate_manifests(self):
        for rows in ([], [{"id": "../outside", "text": "x"}], [{"id": "a", "text": ""}],
                     [{"text": "x"}], [{"id": "a", "text": 3}]):
            with self.subTest(rows=rows):
                manifest = self.manifest("bad.jsonl", rows)
                with self.assertRaises(ValueError):
                    read_manifests([manifest])
        manifest = self.manifest("duplicate.jsonl", [{"id": "a", "text": "a"}])
        with self.assertRaises(ValueError):
            read_manifests([manifest, manifest])

    def test_close_failure_removes_output(self):
        manifest = self.manifest("manifest.jsonl", [{"id": "a", "text": "a"}])
        self.wav("a")
        real_open = Path.open

        @contextmanager
        def fail_on_close(*args, **kwargs):
            with real_open(self.output, *args, **kwargs) as destination:
                yield destination
            raise OSError("close failed")

        def open_file(path, *args, **kwargs):
            if path == self.output:
                return fail_on_close(*args, **kwargs)
            return real_open(path, *args, **kwargs)

        with patch.object(Path, "open", autospec=True, side_effect=open_file):
            with patch("write_reference.HaasFullOnnx") as constructor:
                constructor.return_value.transcribe.return_value = ("a", [], [])
                with self.assertRaisesRegex(OSError, "close failed"):
                    write_reference(self.directory, "encoder.onnx", [manifest], self.directory, self.output)
        self.assertFalse(self.output.exists())

    def test_rejects_wrong_audio_format_and_truncated_wav(self):
        for options in ({"channels": 2}, {"rate": 8000}, {"width": 1}):
            with self.subTest(options=options):
                with self.assertRaises(ValueError):
                    read_wav(self.wav("bad", **options))
        path = self.wav("truncated")
        path.write_bytes(path.read_bytes()[:-2])
        with self.assertRaisesRegex(ValueError, "truncated"):
            read_wav(path)


if __name__ == "__main__":
    unittest.main()
