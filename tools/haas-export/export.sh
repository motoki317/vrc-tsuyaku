#!/usr/bin/env bash
# Run ./export.sh from tools/haas-export/ on Linux inside the vrc-tsuyaku checkout.
# The export group includes CUDA and Triton for Haas-ASR's flash-linear-attention import.
#
# Set HAAS_NEMO to a local .nemo checkpoint from third-intelligence/Haas-ja-v1.0.
# Set HAAS_CHECKPOINT_REVISION to the 40-character lowercase hex commit used to download that checkpoint from Hugging Face.
# Set HAAS_BUNDLE to a new empty output directory outside the checkout.
# Export and quantization load large models and take several minutes.
# If a step fails, start over with a new empty HAAS_BUNDLE because the exporters refuse to overwrite files.
#
# licenses/Apache-2.0.txt comes from the pinned Haas-ASR checkout, which has no NOTICE file.
# If a bundle omits an encoder variant, remove its files from the bundle README's encoder list.

set -eu

: "${HAAS_NEMO:?Set HAAS_NEMO to the local .nemo checkpoint}"
: "${HAAS_CHECKPOINT_REVISION:?Set HAAS_CHECKPOINT_REVISION to the full Hugging Face checkpoint commit}"
: "${HAAS_BUNDLE:?Set HAAS_BUNDLE to a new empty output directory outside the checkout}"

if [[ ! $HAAS_CHECKPOINT_REVISION =~ ^[0123456789abcdef]{40}$ ]]; then
    printf '%s\n' 'Set HAAS_CHECKPOINT_REVISION to the full Hugging Face checkpoint commit (40 lowercase hex characters).' >&2
    exit 1
fi

shopt -s dotglob nullglob
bundle_entries=("$HAAS_BUNDLE"/*)
if ((${#bundle_entries[@]})); then
    printf '%s\n' 'HAAS_BUNDLE must be empty before export.' >&2
    exit 1
fi

uv run --locked --group export python export_onnx.py "$HAAS_NEMO" "$HAAS_BUNDLE"
uv run --locked --group export python export_onnx_decoder.py "$HAAS_NEMO" "$HAAS_BUNDLE"
uv run --locked python quantize_onnx.py "$HAAS_BUNDLE"
uv run --locked python export_vocab.py "$HAAS_BUNDLE/tokenizer.model" "$HAAS_BUNDLE/vocab.txt"
cp licenses/Apache-2.0.txt "$HAAS_BUNDLE/LICENSE"
HAAS_CHECKPOINT_SHA256=$(sha256sum < "$HAAS_NEMO")
HAAS_CHECKPOINT_SHA256=${HAAS_CHECKPOINT_SHA256%% *}
HAAS_EXPORT_COMMIT=$(git rev-parse HEAD)
cat > "$HAAS_BUNDLE/README.md" <<EOF
# Haas-ja-v1.0 ONNX bundle

Converted weights from [third-intelligence/Haas-ja-v1.0](https://huggingface.co/third-intelligence/Haas-ja-v1.0/tree/$HAAS_CHECKPOINT_REVISION).

- Checkpoint revision: $HAAS_CHECKPOINT_REVISION
- Checkpoint SHA-256: $HAAS_CHECKPOINT_SHA256
- Export commit: $HAAS_EXPORT_COMMIT
- Encoder files included: encoder.onnx and encoder.onnx.data (fp32), encoder.int8.onnx (int8).

License: [Apache-2.0](LICENSE).

[Export tool and runtime contract](https://github.com/motoki317/vrc-tsuyaku/tree/$HAAS_EXPORT_COMMIT/tools/haas-export)
EOF
