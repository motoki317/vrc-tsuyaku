# Artifact pins

`pins.go` holds the download URLs, source sizes and SHA-256 values, selected archive members, installed file names, languages and licenses.

When changing a pin:

1. Select an immutable commit for each Hugging Face model. Read its file sizes and LFS SHA-256 values from `https://huggingface.co/api/models/<repo>/tree/<commit>`.
2. For ordinary Git files, download the bytes at that commit and compare their Git blob IDs with the API values. Record their sizes and SHA-256 values.
3. Download each GitHub release archive. Compare its size and SHA-256 with the release API, then hash each selected member from that verified archive.
4. Download the onnx-asr wheel and verify its size and SHA-256 against `https://pypi.org/pypi/onnx-asr/<version>/json`. Extract `onnx_asr/preprocessors/data/nemo128.onnx` from that verified wheel. Compute the member's size and SHA-256 from those extracted bytes for the catalog. The conversion repository's own graph differs from this feature extractor.
5. Download the Silero graph at the selected tag commit. Compute its size and SHA-256 from the downloaded bytes.

For an artifact with an `AcceptanceKey`, the UI shows `License.Text` before a download starts.
After the user accepts the license, the UI saves an RFC3339 timestamp under that key in `config.Settings.LicensesAccepted` through `config.Save(dataDir, settings)`.
`Download` returns `ErrLicenseRequired` if the saved settings lack that key.
The catalog embeds the complete pinned PLaMo license for this purpose.
