# Uses Haas-ASR (Apache-2.0), commit acc614f68769dcbfae9dba96b48439c4a66295af,
# https://github.com/third-intelligence/Haas-ASR, and NeMo 2.7.3 (Apache-2.0),
# https://github.com/NVIDIA/NeMo. This exporter calls their APIs. No upstream
# source code is copied. See ../../THIRD_PARTY_NOTICES.md.
"""Export the fp32 encoder and extract the SentencePiece tokenizer on the CPU."""

import argparse
import tarfile
from pathlib import Path
from tempfile import TemporaryDirectory


def read_tokenizer(nemo_path: Path) -> bytes:
    with tarfile.open(nemo_path) as archive:
        members = [
            member for member in archive.getmembers()
            if member.isfile() and member.name.endswith("_tokenizer.model")
        ]
        if len(members) != 1:
            raise ValueError("expected exactly one *_tokenizer.model file in the .nemo archive")
        with archive.extractfile(members[0]) as tokenizer:
            return tokenizer.read()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("nemo_path", type=Path)
    parser.add_argument("out_dir", type=Path)
    args = parser.parse_args()
    out = args.out_dir
    out.mkdir(parents=True, exist_ok=True)
    for name in ("encoder.onnx", "encoder.onnx.data", "tokenizer.model"):
        if (out / name).exists():
            parser.error(f"{out / name} already exists. Use a fresh output directory.")
    tokenizer = read_tokenizer(args.nemo_path)

    import haas_asr
    import onnx
    import torch

    model = haas_asr.load_model(str(args.nemo_path), device="cpu", dtype=torch.float32)
    # NeMo can emit one sidecar per tensor. One sidecar keeps the bundle portable.
    with TemporaryDirectory(prefix="encoder-", dir=out) as tmp:
        encoder = Path(tmp) / "encoder.onnx"
        model.encoder.export(str(encoder), onnx_opset_version=17)
        graph = onnx.load(str(encoder), load_external_data=True)
        onnx.save(
            graph, str(out / "encoder.onnx"), save_as_external_data=True,
            all_tensors_to_one_file=True, location="encoder.onnx.data", size_threshold=1024,
        )
    (out / "tokenizer.model").write_bytes(tokenizer)
    for name in ("encoder.onnx", "encoder.onnx.data", "tokenizer.model"):
        print(name, (out / name).stat().st_size)


if __name__ == "__main__":
    main()
