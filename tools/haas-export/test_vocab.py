"""Vocabulary format tests and the gated vocabulary test, which skips without its environment variables."""

import itertools
import json
import os
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import Mock, patch

import sentencepiece as spm

from export_vocab import write_vocab


def decode_pieces(pieces, ids):
    data = bytearray()
    for token_id in ids:
        piece = pieces[token_id]
        if re.fullmatch(r"<0x[0-9A-Fa-f]{2}>", piece):
            data.append(int(piece[3:5], 16))
        else:
            if piece == "<unk>":
                piece = " \u2047 "
            data.extend(piece.replace("\u2581", " ").encode("utf-8"))
    # surrogateescape retains each invalid byte, unlike the replace handler.
    text = data.decode("utf-8", errors="surrogateescape")
    return re.sub(r"[\udc80-\udcff]", "\ufffd", text).strip()


class VocabularyTests(unittest.TestCase):
    def test_detokenization_special_pieces(self):
        cases = [
            (["<unk>"], "\u2047"),
            (["left", "<unk>", "right"], "left \u2047 right"),
            (["<0xE3>", "<0x81>"], "\ufffd\ufffd"),
            (["<0xE3>", "<0x81>", "<0x81>", "<0x82>"], "\u3041\ufffd"),
        ]
        for pieces, expected in cases:
            with self.subTest(pieces=pieces):
                self.assertEqual(decode_pieces(pieces, range(len(pieces))), expected)

    def tokenizer(self, size=3072):
        tokenizer = Mock()
        tokenizer.get_piece_size.return_value = size
        special = {0: "<unk>", 1: "\u2581hello", 2: "<0xE3>"}
        tokenizer.id_to_piece.side_effect = lambda i: special.get(i, f"piece{i}")
        return tokenizer

    def test_ordered_entries_and_blank(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "vocab.txt"
            with patch("export_vocab.spm.SentencePieceProcessor", return_value=self.tokenizer()):
                write_vocab(Path("unused.model"), output)
            lines = output.read_text(encoding="utf-8").splitlines()
            self.assertEqual(len(lines), 3073)
            self.assertEqual([int(line.rsplit(" ", 1)[1]) for line in lines], list(range(3073)))
            self.assertEqual(lines[:3], ["<unk> 0", "\u2581hello 1", "<0xE3> 2"])
            self.assertEqual(lines[-1], "<blk> 3072")
            self.assertTrue(output.read_bytes().endswith(b"\n"))

    def test_rejects_wrong_size_and_line_breaks_before_writing(self):
        bad_piece = self.tokenizer()
        bad_piece.id_to_piece.side_effect = lambda _: "bad\npiece"
        for tokenizer in (self.tokenizer(0), self.tokenizer(3071), self.tokenizer(3073), bad_piece):
            with self.subTest(tokenizer=tokenizer), tempfile.TemporaryDirectory() as tmp:
                output = Path(tmp) / "vocab.txt"
                with patch("export_vocab.spm.SentencePieceProcessor", return_value=tokenizer):
                    with self.assertRaises(ValueError):
                        write_vocab(Path("unused.model"), output)
                self.assertFalse(output.exists())

    def test_never_overwrites_an_existing_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "tokenizer.model"
            path.write_bytes(b"keep tokenizer")
            with patch("export_vocab.spm.SentencePieceProcessor", return_value=self.tokenizer()):
                with self.assertRaises(FileExistsError):
                    write_vocab(path, path)
            self.assertEqual(path.read_bytes(), b"keep tokenizer")


class GatedVocabularyTests(unittest.TestCase):
    def test_encoded_manifest_texts(self):
        tokenizer_path = os.environ.get("VRC_TSUYAKU_TEST_HAAS_TOKENIZER")
        manifest_path = os.environ.get("VRC_TSUYAKU_TEST_HAAS_MANIFEST")
        if not tokenizer_path or not manifest_path:
            self.skipTest("set VRC_TSUYAKU_TEST_HAAS_TOKENIZER and VRC_TSUYAKU_TEST_HAAS_MANIFEST")
        tokenizer = spm.SentencePieceProcessor(model_file=tokenizer_path)
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "vocab.txt"
            write_vocab(Path(tokenizer_path), output)
            rows = [line.rsplit(" ", 1) for line in output.read_text(encoding="utf-8").splitlines()]
        self.assertEqual(len(rows), 3073)
        self.assertEqual([int(row[1]) for row in rows], list(range(3073)))
        self.assertEqual(rows[-1], ["<blk>", "3072"])
        pieces = [row[0] for row in rows]
        self.assertEqual(pieces[:-1], [tokenizer.id_to_piece(i) for i in range(3072)])
        with open(manifest_path, encoding="utf-8") as manifest:
            texts = [json.loads(line)["text"] for line in itertools.islice(manifest, 20)]
        self.assertEqual(len(texts), 20, "the manifest must contain at least 20 texts")
        for index, text in enumerate(texts):
            ids = tokenizer.encode(text)
            with self.subTest(manifest_text=index):
                self.assertEqual(decode_pieces(pieces, ids), tokenizer.decode(ids).strip())

        # These sequences cover byte fallback even if all manifest texts use normal pieces.
        byte_ids = [tokenizer.piece_to_id(f"<0x{value:02X}>") for value in "\U0001f9ea".encode()]
        self.assertTrue(all(tokenizer.is_byte(token_id) for token_id in byte_ids))
        boundary = tokenizer.piece_to_id("\u2581")
        self.assertEqual(tokenizer.id_to_piece(boundary), "\u2581")
        sequences = [[], [boundary], byte_ids, [boundary, *byte_ids, boundary],
                     tokenizer.encode("literal <0x41>"),
                     [*tokenizer.encode("hello"), *byte_ids, *tokenizer.encode("world")]]
        for ids in sequences:
            with self.subTest(boundary_ids=ids):
                self.assertEqual(decode_pieces(pieces, ids), tokenizer.decode(ids).strip())

        cases = [
            ([tokenizer.unk_id()], "\u2047"),
            ([*tokenizer.encode("left"), tokenizer.unk_id(), *tokenizer.encode("right")], None),
            ([tokenizer.piece_to_id("<0xE3>"), tokenizer.piece_to_id("<0x81>")],
             "\ufffd\ufffd"),
            ([tokenizer.piece_to_id(piece) for piece in ("<0xE3>", "<0x81>", "<0x81>", "<0x82>")],
             "\u3041\ufffd"),
        ]
        for ids, expected in cases:
            with self.subTest(special_piece_ids=ids):
                decoded = decode_pieces(pieces, ids)
                self.assertEqual(decoded, tokenizer.decode(ids).strip())
                if expected is not None:
                    self.assertEqual(decoded, expected)


if __name__ == "__main__":
    unittest.main()
