"""Write CPU parity references with HaasFullOnnx on four threads."""

import argparse
import json
from pathlib import Path
import sys
import wave

import numpy as np

from haas_full_onnx import HaasFullOnnx


def read_manifests(paths):
    rows = []
    seen = set()
    for path in paths:
        with path.open(encoding="utf-8") as source:
            for number, line in enumerate(source, 1):
                row = json.loads(line)
                clip_id, text = row.get("id"), row.get("text")
                if (not isinstance(clip_id, str) or not clip_id or clip_id in (".", "..")
                        or any(c in clip_id for c in "/\\") or clip_id in seen
                        or not isinstance(text, str) or not text):
                    raise ValueError(f"{path}:{number}: expected a unique filename id and nonempty text")
                seen.add(clip_id)
                rows.append((clip_id, text))
    if not rows:
        raise ValueError("manifests contain no clips")
    return rows


def read_wav(path):
    with wave.open(str(path), "rb") as source:
        if (source.getnchannels() != 1 or source.getframerate() != 16000
                or source.getsampwidth() != 2 or source.getcomptype() != "NONE"):
            raise ValueError(f"{path}: expected mono 16 kHz PCM16 WAV")
        frames = source.getnframes()
        data = source.readframes(frames)
    if not frames or len(data) != frames * 2:
        raise ValueError(f"{path}: empty or truncated WAV")
    return np.frombuffer(data, dtype="<i2").astype(np.float32) / 32768


def write_reference(bundle, encoder, manifests, wav_dir, output):
    rows = read_manifests(manifests)
    # Exclusive creation rejects retries before loading the encoder. Remove an
    # incomplete reference so a shorter corpus cannot silently pass parity.
    destination = output.open("x", encoding="utf-8", newline="\n")
    try:
        with destination as writer:
            model = HaasFullOnnx(bundle, threads=4, encoder_file=encoder)
            for count, (clip_id, text) in enumerate(rows, 1):
                hyp, _, _ = model.transcribe(read_wav(wav_dir / f"{clip_id}.wav"))
                writer.write(json.dumps({"id": clip_id, "hyp": hyp, "ref": text}, ensure_ascii=False) + "\n")
                if count % 50 == 0 or count == len(rows):
                    print(f"{count}/{len(rows)} transcribed", file=sys.stderr, flush=True)
    except BaseException:
        output.unlink()
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", type=Path, required=True)
    parser.add_argument("--encoder", required=True, help="encoder filename in the bundle")
    parser.add_argument("--wav-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True, help="new reference JSONL file")
    parser.add_argument("manifests", type=Path, nargs="+", help="JSONL files with id and text fields")
    args = parser.parse_args()
    write_reference(args.bundle, args.encoder, args.manifests, args.wav_dir, args.output)


if __name__ == "__main__":
    main()
