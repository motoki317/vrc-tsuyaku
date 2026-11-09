# Reimplements Haas-ASR's prediction-network design (Apache-2.0), commit
# acc614f68769dcbfae9dba96b48439c4a66295af, https://github.com/third-intelligence/Haas-ASR.
# Reimplements NeMo 2.7.3 FilterbankFeatures (Apache-2.0), https://github.com/NVIDIA/NeMo,
# and flash-linear-attention 0.5.2 KDA recurrence, ShortConvolution.step and
# FusedRMSNormGated (MIT), https://github.com/fla-org/flash-linear-attention.
# No upstream source code or Triton kernels are copied.
# See ../../THIRD_PARTY_NOTICES.md.
"""Export the prediction step, joint network and log-mel features on the CPU."""

import argparse
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("nemo_path", type=Path)
    parser.add_argument("out_dir", type=Path)
    args = parser.parse_args()
    nemo_path, out = str(args.nemo_path), args.out_dir
    out.mkdir(parents=True, exist_ok=True)
    for name in ("decoder_step.onnx", "joint.onnx", "features.onnx"):
        if (out / name).exists():
            parser.error(f"{out / name} already exists. Use a fresh output directory.")

    import haas_asr
    import numpy as np
    import torch
    import torch.nn.functional as F
    from torch import nn

    model = haas_asr.load_model(nemo_path, device="cpu", dtype=torch.float32)
    pred = model.decoder.prediction
    kda_layers = pred["dec_rnn"].layers
    H, K = kda_layers[0].num_heads, kda_layers[0].head_k_dim
    V = kda_layers[0].head_v_dim
    KD = H * K
    W = kda_layers[0].conv_size
    L = len(kda_layers)


    class KDAStep(nn.Module):
        """One label through embedding -> L x (LayerNorm -> KDA -> residual) -> LayerNorm -> joint.pred."""

        def __init__(self):
            super().__init__()
            self.embed = pred["embed"]
            self.kda = kda_layers
            self.norms = pred["dec_rnn"].norms
            self.final_norm = pred["dec_rnn"].final_norm
            self.pred = model.joint.pred
            self.conv_w = nn.Parameter(torch.stack([
                torch.cat([m.q_conv1d.weight[:, 0], m.k_conv1d.weight[:, 0], m.v_conv1d.weight[:, 0]])
                for m in kda_layers]), requires_grad=False)  # [L, 3*KD, W]

        def forward(self, label, conv_state, rec_state):
            x = self.embed(label)  # [B, 640]
            B = x.shape[0]
            new_conv, new_rec = [], []
            for i, m in enumerate(self.kda):
                h = self.norms[i](x)
                qkv = torch.cat([m.q_proj(h), m.k_proj(h), m.v_proj(h)], dim=-1)  # [B, 3*KD]
                cs = torch.cat([conv_state[i, :, :, 1:], qkv.unsqueeze(-1)], dim=-1)  # [B, 3*KD, W]
                new_conv.append(cs)
                qkv = F.silu((cs * self.conv_w[i]).sum(-1))
                q, k, v = qkv.split([KD, KD, H * V], dim=-1)
                q = q.reshape(B, H, K)
                k = k.reshape(B, H, K)
                v = v.reshape(B, H, V)
                q = q / torch.sqrt((q * q).sum(-1, keepdim=True) + 1e-6) * (K ** -0.5)
                k = k / torch.sqrt((k * k).sum(-1, keepdim=True) + 1e-6)
                g = m.f_proj(h).reshape(B, H, K) + m.dt_bias.reshape(H, K)
                gk = -torch.exp(m.A_log).reshape(H, 1) * F.softplus(g)  # log decay along K
                beta = torch.sigmoid(m.b_proj(h))  # [B, H]
                S = rec_state[i] * torch.exp(gk).unsqueeze(-2)  # [B, H, V, K]
                v = (v - torch.matmul(S, k.unsqueeze(-1)).squeeze(-1)) * beta.unsqueeze(-1)
                S = S + v.unsqueeze(-1) * k.unsqueeze(-2)
                new_rec.append(S)
                o = torch.matmul(S, q.unsqueeze(-1)).squeeze(-1)  # [B, H, V]
                gate = m.g_proj(h).reshape(B, H, V)
                o = o * torch.rsqrt((o * o).mean(-1, keepdim=True) + m.o_norm.eps) * m.o_norm.weight
                o = o * torch.sigmoid(gate)
                x = x + m.o_proj(o.reshape(B, H * V))
            x = self.final_norm(x)
            return self.pred(x), torch.stack(new_conv), torch.stack(new_rec)


    class Joint(nn.Module):
        def __init__(self):
            super().__init__()
            self.enc = model.joint.enc
            self.net = model.joint.joint_net

        def forward(self, enc_frame, dec_out):
            return self.net(self.enc(enc_frame) + dec_out)


    class Features(nn.Module):
        """NeMo FilterbankFeatures (eval) with the STFT as a strided Conv1d over a windowed DFT basis."""

        def __init__(self):
            super().__init__()
            fz = model.preprocessor.featurizer
            n_fft, hop = fz.n_fft, fz.hop_length
            win = torch.zeros(n_fft)
            left = (n_fft - fz.win_length) // 2
            win[left:left + fz.win_length] = fz.window.float()
            n = torch.arange(n_fft, dtype=torch.float64)
            f = torch.arange(n_fft // 2 + 1, dtype=torch.float64)
            ang = 2 * np.pi * f[:, None] * n[None, :] / n_fft
            basis = torch.cat([torch.cos(ang), -torch.sin(ang)]) * win.double()  # [2*257, 512]
            self.register_buffer("basis", basis.float().unsqueeze(1))
            self.register_buffer("fb", fz.fb.float()[0])  # [128, 257]
            self.hop, self.pad, self.nbin = hop, n_fft // 2, n_fft // 2 + 1
            self.preemph = fz.preemph
            self.guard = float(fz.log_zero_guard_value)

        def forward(self, audio):
            n = audio.shape[1]
            seq_len = torch.div(n, self.hop, rounding_mode="floor")
            x = torch.cat([audio[:, :1], audio[:, 1:] - self.preemph * audio[:, :-1]], dim=1)
            x = F.pad(x.unsqueeze(1), (self.pad, self.pad))
            spec = F.conv1d(x, self.basis, stride=self.hop)  # [1, 514, frames]
            re, im = spec[:, : self.nbin], spec[:, self.nbin:]
            mel = torch.log(torch.matmul(self.fb, re * re + im * im) + self.guard)  # [1, 128, frames]
            valid = (torch.arange(mel.shape[2]) < seq_len).to(mel.dtype)  # [frames]
            cnt = valid.sum()
            mean = (mel * valid).sum(-1, keepdim=True) / cnt
            var = (((mel - mean) * valid) ** 2).sum(-1, keepdim=True) / (cnt - 1)
            mel = (mel - mean) / (torch.sqrt(var) + 1e-5)
            return mel * valid


    step, joint, feats = KDAStep().eval(), Joint().eval(), Features().eval()
    B = 1
    blank = model.decoder.blank_idx
    zc = torch.zeros(L, B, 3 * KD, W)
    zr = torch.zeros(L, B, H, V, K)

    with torch.no_grad():
        torch.onnx.export(step, (torch.tensor([blank]), zc, zr), str(out / "decoder_step.onnx"),
                          input_names=["label", "conv_state", "rec_state"],
                          output_names=["dec_out", "conv_state_out", "rec_state_out"],
                          dynamic_axes={"label": {0: "B"}, "conv_state": {1: "B"}, "rec_state": {1: "B"},
                                        "dec_out": {0: "B"}, "conv_state_out": {1: "B"},
                                        "rec_state_out": {1: "B"}},
                          opset_version=17, dynamo=False)
        torch.onnx.export(joint, (torch.zeros(B, 1024), torch.zeros(B, 640)), str(out / "joint.onnx"),
                          input_names=["enc_frame", "dec_out"], output_names=["logits"],
                          dynamic_axes={"enc_frame": {0: "B"}, "dec_out": {0: "B"}, "logits": {0: "B"}},
                          opset_version=17, dynamo=False)
        torch.onnx.export(feats, (torch.zeros(1, 32000),), str(out / "features.onnx"),
                          input_names=["audio"], output_names=["mel"],
                          dynamic_axes={"audio": {1: "N"}, "mel": {2: "frames"}},
                          opset_version=17, dynamo=False)
    for f in ("decoder_step.onnx", "joint.onnx", "features.onnx"):
        print(f, (out / f).stat().st_size)


if __name__ == "__main__":
    main()
