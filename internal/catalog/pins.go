// Uses onnx-asr 0.12.0's Parakeet file names (MIT), https://github.com/istupakov/onnx-asr.
// See ../../THIRD_PARTY_NOTICES.md.

package catalog

const (
	haasBase       = "https://huggingface.co/motoki317/Haas-ja-v1.0-onnx/resolve/1cd4824d0f733b67bb4820adfdd0a38a0f17a55b/"
	parakeetBase   = "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/resolve/0bbb45a3365852604aef28b538a8f066f4ccaa85/"
	parakeetV3Base = "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v3-onnx/resolve/8f23f0c03c8761650bdb5b40aaf3e40d2c15f1ce/"
	plamoBase      = "https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/resolve/" + plamoRevision + "/"
	plamo08Base    = "https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF/resolve/1cf96ae02133958a20074bfdfab76517bb737d00/"
)

func plamoArtifact(name, base, file string, size int64, hash string) entry {
	return entry{Artifact: Artifact{Name: name, Languages: targetCodes, AcceptanceKey: PLaMoAcceptanceKey,
		Licenses: []License{{Name: "PLaMo Community License", URL: base + "LICENSE/en", Covers: "PLaMo model", Text: plamoLicense, Credit: "Built with PLaMo"}},
		Sources:  []Source{plain(base, file, "model", size, hash)},
	}}
}

var artifacts = map[string]entry{
	Haas: {referenceEncoder: "encoder.onnx", Artifact: Artifact{Name: "Haas Japanese int8", Languages: recognizerLanguages(Haas),
		Licenses: []License{{Name: "Apache-2.0", URL: haasBase + "LICENSE", Covers: "Haas model and export"}},
		Sources: []Source{
			plain(haasBase, "features.onnx", "features", 1192336, "e7eab6e413919b88a62a697dc3821646491635e431d587bced33c8b888550d53"),
			plain(haasBase, "encoder.int8.onnx", "encoder", 654132121, "46e46bce6a4ee2b60abc5075dcfc0c76582b6558fa4cba525aa85282612130af"),
			plain(haasBase, "decoder_step.onnx", "decoder", 17145714, "3997b4068cee928fba00455605c52e8fcbcee7a788f23b784bd5bc082872fca8"),
			plain(haasBase, "joint.onnx", "joint", 10516603, "23f5699bf6911483ed9c361e594e7858fba3352f658e39603526442c95d41fca"),
			plain(haasBase, "vocab.txt", "vocabulary", 28704, "ec78b13c5d9f156cf0d5fdbd6b5aec5785680f319c92630c11d5d11b14b83a84"),
			plain(haasBase, "LICENSE", "", 11358, "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"),
		}}},
	Parakeet: {Artifact: Artifact{Name: "Parakeet TDT 0.6B v2 int8", Languages: recognizerLanguages(Parakeet),
		Licenses: []License{{Name: "CC-BY-4.0", URL: "https://creativecommons.org/licenses/by/4.0/", Covers: "NVIDIA weights and ONNX conversion"}, {Name: "MIT", URL: "https://github.com/istupakov/onnx-asr/blob/v0.12.0/LICENSE", Covers: "onnx-asr feature extractor"}},
		Sources: []Source{
			plain(parakeetBase, "encoder-model.int8.onnx", "encoder", 652184014, "3e0581fda6ab843888b51e56d7ee78b6d5bc3237ec113af1f732d1d5286aa155"),
			plain(parakeetBase, "decoder_joint-model.int8.onnx", "decoder_joint", 8998286, "a449f49acd68979d418651dd2dcb737cc0f1bf0225e009e29ee326354edbf7d3"),
			plain(parakeetBase, "vocab.txt", "vocabulary", 9384, "ec182b70dd42113aff6c5372c75cac58c952443eb22322f57bbd7f53977d497d"),
			{URL: "https://files.pythonhosted.org/packages/6a/60/2fa469a2ee674c35ab48821a1039762ae7b9d0b88188ac1012e779477f76/onnx_asr-0.12.0-py3-none-any.whl", Format: "zip", Size: 3980006, SHA256: "5e7ceca454609819ea7833f61e2302e0c8f6ece4f8a78b66c5daba53cb51de4a", Files: []File{
				{Path: "nemo128.onnx", Role: "features", Member: "onnx_asr/preprocessors/data/nemo128.onnx", Size: 138824, SHA256: "5b4a84c52eeaa615dc46d781cc7e4598f9b432184831d57493c48adcd01371a9"},
			}},
		}}},
	ParakeetV3: {Artifact: Artifact{Name: "Parakeet TDT 0.6B v3 int8", Languages: recognizerLanguages(ParakeetV3),
		Licenses: []License{{Name: "CC-BY-4.0", URL: "https://creativecommons.org/licenses/by/4.0/", Covers: "NVIDIA weights and ONNX conversion"}, {Name: "MIT", URL: "https://github.com/istupakov/onnx-asr/blob/v0.12.0/LICENSE", Covers: "onnx-asr feature extractor"}},
		Sources: []Source{
			plain(parakeetV3Base, "encoder-model.int8.onnx", "encoder", 652183999, "6139d2fa7e1b086097b277c7149725edbab89cc7c7ae64b23c741be4055aff09"),
			plain(parakeetV3Base, "decoder_joint-model.int8.onnx", "decoder_joint", 18202004, "eea7483ee3d1a30375daedc8ed83e3960c91b098812127a0d99d1c8977667a70"),
			plain(parakeetV3Base, "vocab.txt", "vocabulary", 93939, "d58544679ea4bc6ac563d1f545eb7d474bd6cfa467f0a6e2c1dc1c7d37e3c35d"),
			plain(parakeetV3Base, "nemo128.onnx", "features", 139764, "a9fde1486ebfcc08f328d75ad4610c67835fea58c73ba57e3209a6f6cf019e9f"),
		}}},
	Silero: {Artifact: Artifact{Name: "Silero VAD v6.2.3", Licenses: []License{{Name: "MIT", URL: "https://github.com/snakers4/silero-vad/blob/5cd7945676eb32225748052e2e6a0580e4686a08/LICENSE", Covers: "Silero graph"}}, Sources: []Source{
		plain("https://raw.githubusercontent.com/snakers4/silero-vad/5cd7945676eb32225748052e2e6a0580e4686a08/src/silero_vad/data/", "silero_vad.onnx", "model", 2327524, "1a153a22f4509e292a94e67d6f9b85e8deb25b4988682b7e174c65279d8788e3"),
	}}},
	PLaMo:    plamoArtifact("PLaMo 3 Translate 2B Q4_K_M", plamoBase, "plamo-3-translate-2b-Q4_K_M.gguf", 1807203040, "1ffd9073afcc70e35ccc942428aa2b73d7aa2b9ea779bf504b775369e0276c13"),
	PLaMo08B: plamoArtifact("PLaMo 3 Translate 0.8B Q4_K_M", plamo08Base, "plamo-3-translate-0.8b-Q4_K_M.gguf", 573126848, "70891ec70dc7ab4205745a38307867c2881b99239f20327927633abfe4bc9ec6"),
	LlamaCPP: {Artifact: Artifact{Name: "llama.cpp b12201 CPU", Licenses: []License{{Name: "MIT", URL: "https://github.com/ggml-org/llama.cpp/blob/b12201/LICENSE", Covers: "llama.cpp runtime"}}}, platforms: map[string]platform{
		"linux": {Sources: []Source{{URL: "https://github.com/ggml-org/llama.cpp/releases/download/b12201/llama-b12201-bin-ubuntu-x64.tar.gz", Format: "tar.gz", Size: 23067593, SHA256: "3fb1afbee5711414f00e59068c10602b1d34919aaea7d4253260387e0e6e4ccf", Files: []File{
			{Path: "libggml-cpu-cannonlake.so", Member: "llama-b12201/libggml-cpu-cannonlake.so", Size: 1583456, SHA256: "c4e143051063c954dd2ed293d3d42037b30373c3fb1466a93a969bbbbe9a1476"},
			{Path: "libllama.so.0", Member: "llama-b12201/libllama.so.0.9.0", Size: 5495696, SHA256: "2abbd1d995788ddabc4d0bea4df6011897c3ffe6efb9899dbc1e7ef941584b04"},
			{Path: "libggml-cpu-alderlake.so", Member: "llama-b12201/libggml-cpu-alderlake.so", Size: 1438176, SHA256: "6b2e71b7db59f664f5428ee9aa4a59cbabc7b0f68aa43e1f2f071bfe56886372"},
			{Path: "libggml-cpu-zen4.so", Member: "llama-b12201/libggml-cpu-zen4.so", Size: 1599616, SHA256: "e5af657a5b69f7c7b8c66c7466e166de995526b8df046237030b4971325156fa"},
			{Path: "libggml-cpu-skylakex.so", Member: "llama-b12201/libggml-cpu-skylakex.so", Size: 1583456, SHA256: "4a2139be8d9e084ec50220a1295c91f436cc84ed38758173e22b5dcf74d7d1de"},
			{Path: "libggml-cpu-icelake.so", Member: "llama-b12201/libggml-cpu-icelake.so", Size: 1599544, SHA256: "776a9e5bfef482824cf7a2fcb206fc086dca5bb46e555134f325e43dec8f326d"},
			{Path: "libllama-common.so.0", Member: "llama-b12201/libllama-common.so.0.9.0", Size: 6783104, SHA256: "cb8f9b025c842ec28ce9d30852ed87fbd3f26ec84dbe9006baad1869cdb8921a"},
			{Path: "libmtmd.so.0", Member: "llama-b12201/libmtmd.so.0.9.0", Size: 2304960, SHA256: "afd0e6d53ddf97b8a12a118f00f0f0fddb86915fdb4cb415104009bedbaf331a"},
			{Path: "libggml-cpu-cascadelake.so", Member: "llama-b12201/libggml-cpu-cascadelake.so", Size: 1599544, SHA256: "eadb9ebc148386e79263bfc1225b14378ced42f0427c4ad45b8049a6e308b418"},
			{Path: "libggml-cpu-haswell.so", Member: "llama-b12201/libggml-cpu-haswell.so", Size: 1438176, SHA256: "d5c208430185a6d4f84d1be9757265f54c890d2ba58a867aa1c957047ac9f3b1"},
			{Path: "libggml-base.so.0", Member: "llama-b12201/libggml-base.so.0.29.0", Size: 946176, SHA256: "0f99690c6e25e57921ff7cd038810c138dd6f4473accbfd212a7bc057b6df050"},
			{Path: "libggml.so.0", Member: "llama-b12201/libggml.so.0.29.0", Size: 51040, SHA256: "8643a58bb37af6fb153e2ea98b48efa227855725ee51c05f4ca76b9867956463"},
			{Path: "libggml-cpu-ivybridge.so", Member: "llama-b12201/libggml-cpu-ivybridge.so", Size: 1373792, SHA256: "6fe4c273e5832563e411b5749be252c7226140cfa67a2eb454e30640109053a0"},
			{Path: "libggml-cpu-sandybridge.so", Member: "llama-b12201/libggml-cpu-sandybridge.so", Size: 1372848, SHA256: "de51eb47a88eb6e77b17662722575b99e4b5ac48326b97026b57c68421c90a08"},
			{Path: "libllama-server-impl.so", Member: "llama-b12201/libllama-server-impl.so", Size: 8296336, SHA256: "9bf6ff4fa4c7527596671aabf7b6c4766539e378ea21fb31f9785e035956c497"},
			{Path: "libggml-cpu-sapphirerapids.so", Member: "llama-b12201/libggml-cpu-sapphirerapids.so", Size: 1865944, SHA256: "c0c6769212a6a129304d1386187677baf81834f53beba85530f6f2c3ff1c3508"},
			{Path: "libggml-cpu-piledriver.so", Member: "llama-b12201/libggml-cpu-piledriver.so", Size: 1369696, SHA256: "05f20153e4b02718ed91c8dbbc05ec810e203f7b7dde0775fa52b0363c25b858"},
			{Path: "libggml-cpu-x64.so", Member: "llama-b12201/libggml-cpu-x64.so", Size: 976512, SHA256: "6c9b84c38e4b6cbc22a8b4f7aff4ce0335c9f67caab8133d86144d59b2d96b1d"},
			{Path: "libggml-cpu-cooperlake.so", Member: "llama-b12201/libggml-cpu-cooperlake.so", Size: 1599616, SHA256: "869b7be246074de644880f099a82f44449d1a69429638e26c13e6779196288d4"},
			{Path: "libggml-rpc.so", Member: "llama-b12201/libggml-rpc.so", Size: 328896, SHA256: "7d0e1cbca812f494de3da87ab17a89899067257d55b3d212d850c9a738ac0f6d"},
			{Path: "libggml-cpu-sse42.so", Member: "llama-b12201/libggml-cpu-sse42.so", Size: 980488, SHA256: "4574298456cd2f28c68d1fdfd66b3016906c1395a694a334dffeb0a938c60474"},
			{Path: "llama-server", Member: "llama-b12201/llama-server", Size: 17864, SHA256: "9b6bff705da86eb73196a38d203002b6137c4b0aaf33d9839b555c4c82372075", Role: "server", Executable: true},
			{Path: "LICENSE", Member: "llama-b12201/LICENSE", Size: 1078, SHA256: "94f29bbed6a22c35b992c5c6ebf0e7c92f13b836b90f36f461c9cf2f0f1d010d"},
		}}}},
		"windows": {Sources: []Source{{URL: "https://github.com/ggml-org/llama.cpp/releases/download/b12201/llama-b12201-bin-win-cpu-x64.zip", Format: "zip", Size: 24758934, SHA256: "62a504f945ea97c9e1842cc2cb81fc64d86b7a668e29c2386cfd4cfe08ddd87d", Files: []File{
			{Path: "ggml-base.dll", Member: "ggml-base.dll", Size: 834048, SHA256: "7c977ad460bac331931267b0a4f46317f45bb511eeca2357b16a4073ed6672d3"},
			{Path: "ggml-cpu-alderlake.dll", Member: "ggml-cpu-alderlake.dll", Size: 1442816, SHA256: "e729a20526895326c1359c44edb5af280132f294ba890d1a560abd6bd52a3951"},
			{Path: "ggml-cpu-cannonlake.dll", Member: "ggml-cpu-cannonlake.dll", Size: 1638400, SHA256: "9a18008835059bcafbc37959c2f9311c8ea4296b60bcd9b4e84f3ef90757c384"},
			{Path: "ggml-cpu-cascadelake.dll", Member: "ggml-cpu-cascadelake.dll", Size: 1611776, SHA256: "1bf07409b305ea41b6759dafe47401aa4293fe087c7989444a480238996c058d"},
			{Path: "ggml-cpu-cooperlake.dll", Member: "ggml-cpu-cooperlake.dll", Size: 1621504, SHA256: "0e172a05f4a264a245e8d75cf00040b4ede6a9ef4cff8bac44c2f7dc6bf20d3d"},
			{Path: "ggml-cpu-haswell.dll", Member: "ggml-cpu-haswell.dll", Size: 1447936, SHA256: "01ed3445f8f39d9140e04b713af2fd58e8c699d809ed0a4a8753951e03d6f0c0"},
			{Path: "ggml-cpu-icelake.dll", Member: "ggml-cpu-icelake.dll", Size: 1618432, SHA256: "581d178fcdab32ff913109ef4ab6919032f3adf53dac9441f326456217a57647"},
			{Path: "ggml-cpu-ivybridge.dll", Member: "ggml-cpu-ivybridge.dll", Size: 1329152, SHA256: "74f221b8ca0b7c9840c7a019c102474b39119b295ee0e7f9fd466517dffdb3ed"},
			{Path: "ggml-cpu-piledriver.dll", Member: "ggml-cpu-piledriver.dll", Size: 1321984, SHA256: "302a3935b5764b5642a9f1f73e76c646f915afe40ec50559fa1a72e07a5c1a14"},
			{Path: "ggml-cpu-sandybridge.dll", Member: "ggml-cpu-sandybridge.dll", Size: 1319936, SHA256: "11749aef9cc6deca67b1b75e2268d52f00dead8b136eca0bea3793a9aa39c499"},
			{Path: "ggml-cpu-sapphirerapids.dll", Member: "ggml-cpu-sapphirerapids.dll", Size: 1897984, SHA256: "a6b2d4a39ad2792bd10ae5ea8faa73e10f38822fc0dfe5b32c247563b086a2cf"},
			{Path: "ggml-cpu-skylakex.dll", Member: "ggml-cpu-skylakex.dll", Size: 1632256, SHA256: "77d1bccfadefc37699bfc4d99213605f4dd35237612e41d0162bb23cc7d90761"},
			{Path: "ggml-cpu-sse42.dll", Member: "ggml-cpu-sse42.dll", Size: 944128, SHA256: "4ae69bc961761cb1fb4509f1fffd80c5846830e617c38b2e41017f61ddecac0f"},
			{Path: "ggml-cpu-x64.dll", Member: "ggml-cpu-x64.dll", Size: 936448, SHA256: "effb354f760529a39200b01c832cba5dba0b734d5707ae632689ad8a98082d7d"},
			{Path: "ggml-cpu-zen4.dll", Member: "ggml-cpu-zen4.dll", Size: 1628160, SHA256: "efead220741aea37c7a187b7802678b3e34e042b02bb87dcfa02c55c77f53322"},
			{Path: "ggml-rpc.dll", Member: "ggml-rpc.dll", Size: 236032, SHA256: "09816d9d926098cac82be6da72e8ee95d44f2e1593b6d547176b5af01a48d1d1"},
			{Path: "ggml.dll", Member: "ggml.dll", Size: 78848, SHA256: "1f1496c2d2b1aa3ad816d4b7b682587c0b6fc8dd5c7919984a3622a46690afa2"},
			{Path: "libomp.dll", Member: "libomp.dll", Size: 768000, SHA256: "a12116ba72d1d6820407cf30be23da04ce79d6bb8a71a5ee71759c5a1faa6f1c"},
			{Path: "LICENSE-LLVM-OpenMP", Member: "LICENSE-LLVM-OpenMP", Size: 19741, SHA256: "fdad1758a9e1f9d5a81e18879b3406772115edc92c24bfa36b70c654f325e8e4"},
			{Path: "llama-common.dll", Member: "llama-common.dll", Size: 8385024, SHA256: "166b7a66cdce3c210c7d72ff38433a84c970e81bff51199ecbf69e8c41bf8156"},
			{Path: "llama-server-impl.dll", Member: "llama-server-impl.dll", Size: 10124800, SHA256: "c96d85208d95fd11f122afd14716604ebc280ab436e6ee5fe4967f273d29545e"},
			{Path: "llama-server.exe", Member: "llama-server.exe", Size: 9728, SHA256: "a670e886f3d3970b17cd97ada8b7b56a5287b53df0fcc2ec6fcc9bbb2256878f", Role: "server", Executable: true},
			{Path: "llama.dll", Member: "llama.dll", Size: 3852288, SHA256: "35e345bbb92aa578e65858715260c659a498a71e530f1e1fd9607bfd22516620"},
			{Path: "mtmd.dll", Member: "mtmd.dll", Size: 2216960, SHA256: "1b144635dd2237dfe7ab4b280246e768e5cec22c016e73b8b3bd2488f57e52aa"},
		}}}, Licenses: []License{{Name: "Apache-2.0 WITH LLVM-exception", URL: "https://github.com/llvm/llvm-project/blob/baf637dcde8ccc24fd6ffbc82d8321ba18ec857f/openmp/LICENSE.TXT", Covers: "Windows OpenMP runtime"}}},
	}},
	ONNXRuntime: {Artifact: Artifact{Name: "ONNX Runtime 1.31.1 CPU", Licenses: []License{{Name: "MIT", URL: "https://github.com/microsoft/onnxruntime/blob/v1.31.1/LICENSE", Covers: "ONNX Runtime"}}}, platforms: map[string]platform{
		"linux": {Sources: []Source{{URL: "https://github.com/microsoft/onnxruntime/releases/download/v1.31.1/onnxruntime-linux-x64-1.31.1.tgz", Format: "tar.gz", Size: 11413655, SHA256: "c849e36a1ef3e22d4ff217b9d1ce4a3cf5b9467b975610913ae959ad5b16bdc9", Files: []File{
			{Path: "libonnxruntime_providers_shared.so", Member: "onnxruntime-linux-x64-1.31.1/lib/libonnxruntime_providers_shared.so", Size: 14632, SHA256: "086ec1d5388f64153d9c63470d126693db9a182c8ce236d3a1119068471b8a0d"},
			{Path: "libonnxruntime.so.1", Member: "onnxruntime-linux-x64-1.31.1/lib/libonnxruntime.so.1.31.1", Size: 29345384, SHA256: "a96f132e5eb5ca04e23a7d2fc1078fb95aa591e8d71d0f237702ece3a1e0f4ea", Role: "runtime"},
			{Path: "ThirdPartyNotices.txt", Member: "onnxruntime-linux-x64-1.31.1/ThirdPartyNotices.txt", Size: 338088, SHA256: "143764b952fdb1a7c69ce653bfba74a7744d6a8a573bfb73e235fba356c83de3"},
			{Path: "LICENSE", Member: "onnxruntime-linux-x64-1.31.1/LICENSE", Size: 1073, SHA256: "2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c"},
		}}}},
		"windows": {Sources: []Source{{URL: "https://github.com/microsoft/onnxruntime/releases/download/v1.31.1/onnxruntime-win-x64-1.31.1.zip", Format: "zip", Size: 82929944, SHA256: "5869e26bb25314375f2d5ef1877e3ea06b1543d6c710e6e4a1ec2f7177ab4b2c", Files: []File{
			{Path: "onnxruntime.dll", Member: "onnxruntime-win-x64-1.31.1/lib/onnxruntime.dll", Size: 17253728, SHA256: "cb2fa518b18356617f6f9f460c012501da2819ec7e5c1d80bd8f5de20492db0c", Role: "runtime"},
			{Path: "onnxruntime_providers_shared.dll", Member: "onnxruntime-win-x64-1.31.1/lib/onnxruntime_providers_shared.dll", Size: 21816, SHA256: "8cea20be64259950cda8643af0ed42c2a424400fc3bb9bb29f9a35cee6b3419f"},
			{Path: "LICENSE", Member: "onnxruntime-win-x64-1.31.1/LICENSE", Size: 1094, SHA256: "c250d6278f0b47a6439fb7592b08b58a55eb9f535aa49a1db63211c3f982b674"},
			{Path: "ThirdPartyNotices.txt", Member: "onnxruntime-win-x64-1.31.1/ThirdPartyNotices.txt", Size: 344457, SHA256: "c53a76501ef60db6f865f20599f220761201ac4057683acefdab37d861b86622"},
		}}}},
	}},
}
