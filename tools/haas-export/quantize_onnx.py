# Uses ONNX Runtime 1.31.1 (MIT), https://github.com/microsoft/onnxruntime.
# This script calls its quantization API. No upstream source code is copied.
# See ../../THIRD_PARTY_NOTICES.md.
"""Derive encoder.int8.onnx from the fp32 encoder with dynamic int8 weights."""

import argparse
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("onnx_dir", type=Path)
    args = parser.parse_args()
    source = args.onnx_dir / "encoder.onnx"
    target = args.onnx_dir / "encoder.int8.onnx"
    if not source.is_file():
        parser.error(f"{source} does not exist. Run export_onnx.py first.")
    if target.exists():
        parser.error(f"{target} already exists. Use a fresh output directory.")

    from onnxruntime.quantization import QuantType, quantize_dynamic

    quantize_dynamic(
        str(source), str(target), weight_type=QuantType.QInt8, per_channel=True,
    )
    print(target.name, target.stat().st_size)


if __name__ == "__main__":
    main()
