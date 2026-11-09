# Reimplements the onnx-asr vocabulary format (MIT), https://github.com/istupakov/onnx-asr.
# Uses SentencePiece (Apache-2.0), https://github.com/google/sentencepiece.
# No upstream source code is copied. See ../../THIRD_PARTY_NOTICES.md.
"""Write Haas SentencePiece entries as <piece> <id> lines, followed by <blk> 3072."""

import argparse
from pathlib import Path

import sentencepiece as spm

from haas_constants import BLANK


def write_vocab(tokenizer_path: Path, output_path: Path) -> None:
    tokenizer = spm.SentencePieceProcessor(model_file=str(tokenizer_path))
    size = tokenizer.get_piece_size()
    if size != BLANK:
        raise ValueError(f"expected {BLANK} Haas tokens, found {size}")
    lines = []
    for token_id in range(size):
        piece = tokenizer.id_to_piece(token_id)
        if "\n" in piece or "\r" in piece:
            raise ValueError(f"token {token_id} contains a line break")
        lines.append(f"{piece} {token_id}\n")
    lines.append(f"<blk> {BLANK}\n")
    # Exclusive creation prevents a mistaken output path from replacing the tokenizer.
    with output_path.open("x", encoding="utf-8", newline="\n") as output:
        output.writelines(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tokenizer", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    try:
        write_vocab(args.tokenizer, args.output)
    except (OSError, RuntimeError, ValueError) as error:
        parser.error(str(error))
    print(f"{args.output}: {BLANK + 1} entries")


if __name__ == "__main__":
    main()
