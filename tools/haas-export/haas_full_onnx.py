# Reimplements NeMo 2.7.3 greedy TDT decoding and short-clip padding (Apache-2.0),
# https://github.com/NVIDIA/NeMo, with the Haas-ASR prediction-state design
# (Apache-2.0), commit acc614f68769dcbfae9dba96b48439c4a66295af,
# https://github.com/third-intelligence/Haas-ASR. No upstream source code is copied.
# Uses ONNX Runtime 1.31.1 (MIT), https://github.com/microsoft/onnxruntime.
# See ../../THIRD_PARTY_NOTICES.md.
"""The Python reference for the Go recognizer, internal/asr.

Outside ONNX, HaasFullOnnx pads audio and mel features, trims and transposes
encoder output, runs greedy TDT decoding, computes token log-probabilities
with log-softmax over the 3073 token scores (without duration scores), and
detokenizes with SentencePiece.
"""
from __future__ import annotations

from pathlib import Path

import numpy as np
import onnxruntime as ort
import sentencepiece as spm

from haas_constants import BLANK

N_TOK = BLANK + 1  # logits[:N_TOK] = tokens + blank, logits[N_TOK:] = durations
DURATIONS = (0, 1, 2, 3, 4)
MAX_SYMBOLS = 10  # per encoder frame
LAYERS, CONV_CH, CONV_W, HEADS, HEAD_DIM = 2, 960, 4, 5, 64


class HaasFullOnnx:
    def __init__(self, model_dir, providers=None, threads=None, encoder_file="encoder.onnx",
                 frame_bucket=16, decoder_providers=None):
        d = Path(model_dir)
        so = ort.SessionOptions()
        if any("Dml" in (p if isinstance(p, str) else p[0]) for p in providers or []):
            so.enable_mem_pattern = False  # DirectML EP requirements
            so.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        if threads:
            so.intra_op_num_threads = threads
            so.inter_op_num_threads = 1
        providers = providers or ["CPUExecutionProvider"]
        # The per-step nets are tiny; on a GPU EP their launch overhead dominates, so they can stay on CPU.
        small = decoder_providers or providers
        self.features_s = ort.InferenceSession(str(d / "features.onnx"), so, providers=small)
        self.encoder = ort.InferenceSession(str(d / encoder_file), so, providers=providers)
        self.step = ort.InferenceSession(str(d / "decoder_step.onnx"), so, providers=small)
        self.joint = ort.InferenceSession(str(d / "joint.onnx"), so, providers=small)
        self.sp = spm.SentencePieceProcessor(model_file=str(d / "tokenizer.model"))
        self.frame_bucket = frame_bucket

    def encode(self, audio: np.ndarray) -> np.ndarray:
        x = np.asarray(audio, dtype=np.float32)
        if len(x) < 16000:  # NeMo transcribe() pads clips under 1 s to 1 s, on both sides
            total = 16000 - len(x)
            x = np.pad(x, (total // 2, total - total // 2))
        (mel,) = self.features_s.run(None, {"audio": x[None]})
        length = len(x) // 160
        pad = (-mel.shape[2]) % self.frame_bucket
        mel = np.pad(mel, ((0, 0), (0, 0), (0, pad)))
        enc, enc_len = self.encoder.run(None, {"audio_signal": mel, "length": np.array([length], np.int64)})
        return enc[0, :, : int(enc_len[0])].T.copy()  # [T, 1024]

    def _predict(self, label, conv, rec):
        return self.step.run(None, {"label": np.array([label], np.int64), "conv_state": conv, "rec_state": rec})

    def decode(self, enc: np.ndarray):
        T = enc.shape[0]
        conv = np.zeros((LAYERS, 1, CONV_CH, CONV_W), np.float32)
        rec = np.zeros((LAYERS, 1, HEADS, HEAD_DIM, HEAD_DIM), np.float32)
        dec_out, conv, rec = self._predict(BLANK, conv, rec)
        tokens, logprobs = [], []
        t, last_t, lasts = 0, -1, 0
        while t < T:
            while True:  # advance over blanks until a token is emitted or the audio ends
                (logits,) = self.joint.run(None, {"enc_frame": enc[t:t + 1], "dec_out": dec_out})
                logits = logits[0]
                tok = int(np.argmax(logits[:N_TOK]))
                dur = DURATIONS[int(np.argmax(logits[N_TOK:]))]
                if tok == BLANK and dur == 0:
                    dur = 1
                t_label, t = t, t + dur
                if tok != BLANK or t >= T:
                    break
            if tok == BLANK:
                break
            z = logits[:N_TOK] - logits[:N_TOK].max()
            logprobs.append(float(z[tok] - np.log(np.exp(z).sum())))
            tokens.append(tok)
            lasts = lasts + 1 if t_label == last_t else 1
            last_t = t_label
            dec_out, conv, rec = self._predict(tok, conv, rec)
            if t < T and lasts >= MAX_SYMBOLS and last_t == t:
                t += 1
        return tokens, logprobs

    def transcribe(self, audio: np.ndarray):
        tokens, logprobs = self.decode(self.encode(audio))
        return self.sp.decode(tokens).strip(), tokens, logprobs
