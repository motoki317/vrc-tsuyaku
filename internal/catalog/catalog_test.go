package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPins(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		list, err := List(goos)
		if err != nil || len(list) != 8 {
			t.Fatalf("catalog: %v, %v", list, err)
		}
		for _, a := range list {
			t.Run(goos+"/"+a.ID, func(t *testing.T) {
				if a.Name == "" || len(a.Sources) == 0 || len(a.Licenses) == 0 {
					t.Fatal("incomplete artifact")
				}
				paths, roles := map[string]bool{}, map[string]bool{}
				for _, l := range a.Licenses {
					if l.Name == "" || l.Covers == "" {
						t.Fatal("incomplete license")
					}
					if a.ID != PLaMo && a.ID != PLaMo08B && l.Credit != "" {
						t.Fatalf("unexpected required credit %q", l.Credit)
					}
					checkHTTPS(t, l.URL)
				}
				for _, s := range a.Sources {
					checkHTTPS(t, s.URL)
					checkHash(t, s.SHA256)
					if s.Size <= 0 || len(s.Files) == 0 {
						t.Fatal("empty source")
					}
					if s.Format != "file" && s.Format != "zip" && s.Format != "tar.gz" {
						t.Fatalf("format %q", s.Format)
					}
					for _, f := range s.Files {
						checkHash(t, f.SHA256)
						if f.Size <= 0 || !filepath.IsLocal(f.Path) || strings.ContainsAny(f.Path, "/\\") || paths[strings.ToLower(f.Path)] {
							t.Fatalf("invalid/duplicate file %+v", f)
						}
						paths[strings.ToLower(f.Path)] = true
						if f.Role != "" {
							if roles[f.Role] {
								t.Fatalf("duplicate role %q", f.Role)
							}
							roles[f.Role] = true
						}
						if s.Format == "file" {
							if len(s.Files) != 1 || f.Member != "" || s.Size != f.Size || s.SHA256 != f.SHA256 {
								t.Fatal("plain file metadata differs")
							}
						} else if !filepath.IsLocal(f.Member) || strings.Contains(f.Member, "\\") {
							t.Fatalf("invalid member %q", f.Member)
						}
					}
				}
			})
		}
	}
	if _, err := Lookup("missing", "linux"); err == nil {
		t.Fatal("unknown ID accepted")
	}
	if _, err := List("darwin"); err == nil {
		t.Fatal("unsupported OS accepted")
	}
}

func checkHTTPS(t *testing.T, s string) {
	t.Helper()
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		t.Fatalf("non-HTTPS URL %q", s)
	}
}
func checkHash(t *testing.T, s string) {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil || len(b) != sha256.Size {
		t.Fatalf("invalid SHA256 %q", s)
	}
}

func TestCopiesAndPaths(t *testing.T) {
	a, _ := Lookup(Haas, "linux")
	original, _ := Lookup(Haas, "linux")
	a.Languages[0] = "changed"
	a.Licenses[0].Name = "changed"
	a.Sources[0].URL = "changed"
	a.Sources[0].Files[0].Path = "changed"
	got, _ := Lookup(Haas, "linux")
	if !reflect.DeepEqual(got, original) {
		t.Fatal("caller mutated catalog")
	}
	dir := "bundle"
	h := HaasBundle(dir, true)
	fp := HaasBundle(dir, false)
	if fp.Encoder == h.Encoder {
		t.Fatal("fp32 encoder missing")
	}
	p := ParakeetBundle(Parakeet, dir)
	v3 := ParakeetBundle(ParakeetV3, dir)
	for _, tc := range []struct{ id, role, path string }{
		{Haas, "features", h.Features}, {Haas, "encoder", h.Encoder}, {Haas, "decoder", h.Decoder}, {Haas, "joint", h.Joint}, {Haas, "vocabulary", h.Vocabulary},
		{Parakeet, "features", p.Features}, {Parakeet, "encoder", p.Encoder}, {Parakeet, "decoder_joint", p.DecoderJoint}, {Parakeet, "vocabulary", p.Vocabulary},
		{ParakeetV3, "features", v3.Features}, {ParakeetV3, "encoder", v3.Encoder}, {ParakeetV3, "decoder_joint", v3.DecoderJoint}, {ParakeetV3, "vocabulary", v3.Vocabulary},
		{Silero, "model", SileroModel(dir)}, {PLaMo, "model", PLaMoModel(dir)},
	} {
		a, _ := Lookup(tc.id, "linux")
		want, err := a.Path(dir, tc.role)
		if err != nil || tc.path != want {
			t.Fatalf("%s %s: %q != %q (%v)", tc.id, tc.role, tc.path, want, err)
		}
	}
	a, _ = Lookup(ONNXRuntime, runtime.GOOS)
	want, err := a.Path(dir, "runtime")
	if err != nil || ORTLibrary(dir) != want {
		t.Fatalf("runtime path: %q != %q (%v)", ORTLibrary(dir), want, err)
	}
	if LlamaServer(dir) == "" {
		t.Fatal("missing executable")
	}
	if _, err := got.Path(dir, "absent"); err == nil {
		t.Fatal("missing role accepted")
	}
}

func TestPLaMoLicenseAvailableOffline(t *testing.T) {
	for _, translator := range Translators() {
		t.Run(translator.ID, func(t *testing.T) {
			a, _ := Lookup(translator.Artifact, "linux")
			if a.AcceptanceKey != "plamo-community-license@2026-11-09" {
				t.Fatalf("license acceptance key = %q", a.AcceptanceKey)
			}
			if len(a.Licenses) != 1 || !strings.Contains(a.Licenses[0].Text, "legal guardian") {
				t.Fatal("full license unavailable before network")
			}
			if a.Licenses[0].Credit != "Built with PLaMo" {
				t.Fatalf("required credit = %q, want Built with PLaMo", a.Licenses[0].Credit)
			}
			sum := sha256.Sum256([]byte(a.Licenses[0].Text))
			if hex.EncodeToString(sum[:]) != "ca0c5e90f6de6799fc2d2d4f2b2647cc088444c19f286f689e525d4ad28f9ed6" {
				t.Fatal("embedded license differs from pinned bytes")
			}
		})
	}
}

func TestPLaMoPin(t *testing.T) {
	for _, tc := range []struct {
		id, base, file, hash string
		size                 int64
	}{
		{PLaMo, "https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/resolve/4f672106b847200bfdc0f6af6c321cd94eba1aed/", "plamo-3-translate-2b-Q4_K_M.gguf", "1ffd9073afcc70e35ccc942428aa2b73d7aa2b9ea779bf504b775369e0276c13", 1807203040},
		{PLaMo08B, "https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF/resolve/1cf96ae02133958a20074bfdfab76517bb737d00/", "plamo-3-translate-0.8b-Q4_K_M.gguf", "70891ec70dc7ab4205745a38307867c2881b99239f20327927633abfe4bc9ec6", 573126848},
	} {
		for _, goos := range []string{"linux", "windows"} {
			t.Run(tc.id+"/"+goos, func(t *testing.T) {
				a, err := Lookup(tc.id, goos)
				if err != nil {
					t.Fatal(err)
				}
				if len(a.Licenses) != 1 || a.Licenses[0].URL != tc.base+"LICENSE/en" {
					t.Fatalf("license URL differs from pinned revision: %+v", a.Licenses)
				}
				if len(a.Sources) != 1 {
					t.Fatalf("model sources = %d, want 1", len(a.Sources))
				}
				s := a.Sources[0]
				if s.URL != tc.base+tc.file || s.Size != tc.size || s.SHA256 != tc.hash {
					t.Fatalf("model pin differs: %+v", s)
				}
			})
		}
	}
}
