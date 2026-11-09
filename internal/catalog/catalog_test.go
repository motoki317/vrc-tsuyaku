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
		if err != nil || len(list) != 7 {
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
					if a.ID != PLaMo && l.Credit != "" {
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
	a, _ := Lookup(PLaMo, "linux")
	if a.AcceptanceKey != PLaMoAcceptanceKey || !strings.Contains(a.AcceptanceKey, plamoRevision) {
		t.Fatal("acceptance revision differs")
	}
	if len(a.Licenses) != 1 || !strings.Contains(a.Licenses[0].Text, "legal guardian") {
		t.Fatal("full license unavailable before network")
	}
	if a.Licenses[0].Credit != "Built with PLaMo" {
		t.Fatalf("required credit = %q, want Built with PLaMo", a.Licenses[0].Credit)
	}
	sum := sha256.Sum256([]byte(a.Licenses[0].Text))
	if hex.EncodeToString(sum[:]) != "d9979039b8f1a96fa81e1be61c4550d06f8542b25119e3f765c7c011a68597fb" {
		t.Fatal("embedded license differs from pinned bytes")
	}
}
