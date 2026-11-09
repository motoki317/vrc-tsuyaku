package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func fixture(body []byte) catalog.Artifact {
	f := catalog.File{Path: "model", Role: "model", Size: int64(len(body)), SHA256: digest(body)}
	return catalog.Artifact{ID: "fixture", Sources: []catalog.Source{{Format: "file", Size: f.Size, SHA256: f.SHA256, Files: []catalog.File{f}}}}
}
func testManager(t *testing.T, handler http.HandlerFunc) (*Manager, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.client.Transport = server.Client().Transport
	t.Cleanup(m.client.CloseIdleConnections)
	return m, server
}
func assertClean(t *testing.T, m *Manager) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(m.dataDir, "downloads"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary files remain: %s", e.Name())
		}
	}
}
func installedBytes(t *testing.T, m *Manager) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.dataDir, "downloads", "fixture", "model"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallReplaceAndManualCopy(t *testing.T) {
	body := []byte("verified model")
	var hits atomic.Int32
	m, s := testManager(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = w.Write(body) })
	a := fixture(body)
	a.Sources[0].URL = s.URL
	var events []Progress
	for range 2 {
		if err := m.install(context.Background(), a, func(p Progress) { events = append(events, p) }); err != nil {
			t.Fatal(err)
		}
		if ok, err := m.check(context.Background(), a); !ok || err != nil {
			t.Fatalf("installed: %v, %v", ok, err)
		}
		assertClean(t, m)
	}
	if hits.Load() != 2 {
		t.Fatal("retry did not start over")
	}
	last := events[len(events)-1]
	if last.Phase != "installed" || last.Downloaded != int64(len(body)) || last.Total != last.Downloaded {
		t.Fatalf("progress: %+v", events)
	}
	// A second manager discovers manually copied bytes without any HTTP requests.
	manual, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(manual.dataDir, "downloads", a.ID)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "model"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := manual.check(context.Background(), a); !ok || err != nil {
		t.Fatalf("manual copy: %v, %v", ok, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "model"), bytes.Repeat([]byte("x"), len(body)), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := manual.check(context.Background(), a); ok || err != nil {
		t.Fatalf("corruption: %v, %v", ok, err)
	}
	if err = os.Remove(filepath.Join(dir, "model")); err != nil {
		t.Fatal(err)
	}
	if ok, err := manual.check(context.Background(), a); ok || err != nil {
		t.Fatalf("missing: %v, %v", ok, err)
	}
}

func TestTransferFailuresPreserveInstalled(t *testing.T) {
	good := []byte("good")
	for _, mode := range []string{"hash", "short", "long", "status", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			m, s := testManager(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "hash":
					_, _ = w.Write([]byte("evil"))
				case "short":
					_, _ = w.Write([]byte("x"))
				case "long":
					w.(http.Flusher).Flush()
					_, _ = w.Write([]byte("too long"))
				case "status":
					http.Error(w, "failure", http.StatusBadGateway)
				case "cancel":
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			})
			a := fixture(good)
			a.Sources[0].URL = s.URL
			dir := filepath.Join(m.dataDir, "downloads", a.ID)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "model"), good, 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Cancel once the HTTP request has started, without relying on a sleep.
			if mode == "cancel" {
				old := m.client.Transport
				m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					resp, err := old.RoundTrip(r)
					cancel()
					return resp, err
				})
			}
			err := m.install(ctx, a, nil)
			if err == nil || strings.Contains(err.Error(), "-data-dir") {
				t.Fatal("failure accepted")
			}
			want := errcode.DownloadIntegrity
			if mode == "status" {
				want = errcode.DownloadHTTP
			}
			if mode != "cancel" && errcode.Of(err) != want {
				t.Fatalf("transfer error: %v (%s), want %s", err, errcode.Of(err), want)
			}
			if !bytes.Equal(installedBytes(t, m), good) {
				t.Fatal("old installation changed")
			}
			assertClean(t, m)
		})
	}
}

type archiveItem struct {
	name string
	data []byte
	link bool
}

func archiveBytes(t *testing.T, format string, items []archiveItem) []byte {
	t.Helper()
	var b bytes.Buffer
	if format == "zip" {
		z := zip.NewWriter(&b)
		for _, item := range items {
			h := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
			h.SetMode(0o644)
			if item.link {
				h.SetMode(os.ModeSymlink | 0o777)
			}
			w, err := z.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(item.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&b)
		tr := tar.NewWriter(gz)
		for _, item := range items {
			h := &tar.Header{Name: item.name, Mode: 0o644, Size: int64(len(item.data)), Typeflag: tar.TypeReg}
			if item.link {
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "outside"
				h.Size = 0
			}
			if err := tr.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if !item.link {
				if _, err := tr.Write(item.data); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tr.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func TestArchives(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		for _, mode := range []string{"valid", "hash", "missing", "link", "duplicate", "cancel"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				body := []byte("member bytes")
				items := []archiveItem{{"nested/selected", body, false}, {"../../escape", []byte("ignored"), false}}
				switch mode {
				case "missing":
					items = items[1:]
				case "link":
					items[0].link = true
				case "duplicate":
					items = append(items, items[0])
				}
				data := archiveBytes(t, format, items)
				m, s := testManager(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) })
				a := fixture(body)
				a.Sources[0].Format = format
				a.Sources[0].URL = s.URL
				a.Sources[0].Size = int64(len(data))
				a.Sources[0].SHA256 = digest(data)
				a.Sources[0].Files[0].Member = "nested/selected"
				a.Sources[0].Files[0].Executable = true
				if mode == "hash" {
					a.Sources[0].Files[0].SHA256 = digest([]byte("wrong"))
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				err := m.install(ctx, a, func(p Progress) {
					if mode == "cancel" && p.Phase == "verify" {
						cancel()
					}
				})
				if mode == "valid" {
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(installedBytes(t, m), body) {
						t.Fatal("wrong member")
					}
					entries, err := os.ReadDir(filepath.Join(m.dataDir, "downloads", a.ID))
					if err != nil || len(entries) != 1 {
						t.Fatalf("unselected files installed: %v, %v", entries, err)
					}
				} else {
					if err == nil {
						t.Fatal("bad archive accepted")
					}
					if _, err = os.Stat(filepath.Join(m.dataDir, "downloads", a.ID)); !os.IsNotExist(err) {
						t.Fatalf("failed install published: %v", err)
					}
				}
				assertClean(t, m)
			})
		}
	}
}

func TestHTTPSRedirects(t *testing.T) {
	var insecureHits atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { insecureHits.Add(1) }))
	defer insecure.Close()
	body := []byte("bytes")
	m, s := testManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/data", http.StatusFound)
		case "/data":
			_, _ = w.Write(body)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.Redirect(w, r, insecure.URL, http.StatusFound)
		}
	})
	for _, tc := range []struct {
		url string
		ok  bool
	}{{s.URL + "/redirect", true}, {s.URL + "/downgrade", false}, {s.URL + "/loop", false}, {insecure.URL, false}} {
		a := fixture(body)
		a.Sources[0].URL = tc.url
		err := m.install(context.Background(), a, nil)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.url, err)
		}
		assertClean(t, m)
	}
	if insecureHits.Load() != 0 {
		t.Fatal("contacted insecure URL")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLicenseGateBeforeNetwork(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	sentinel := errors.New("network reached")
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { hits++; return nil, sentinel })
	ctx := context.Background()
	if err = m.Download(ctx, catalog.PLaMo, "linux", nil); !errors.Is(err, ErrLicenseRequired) || hits != 0 {
		t.Fatalf("missing consent: %v, hits %d", err, hits)
	}
	s := config.Defaults()
	s.LicensesAccepted["plamo-community-license@older"] = "2026-11-09T00:00:00Z"
	if err = config.Save(m.dataDir, s); err != nil {
		t.Fatal(err)
	}
	if err = m.Download(ctx, catalog.PLaMo, "linux", nil); !errors.Is(err, ErrLicenseRequired) || hits != 0 {
		t.Fatalf("stale consent: %v, hits %d", err, hits)
	}
	s.LicensesAccepted[catalog.PLaMoAcceptanceKey] = "2026-11-09T00:00:00Z"
	if err = config.Save(m.dataDir, s); err != nil {
		t.Fatal(err)
	}
	if err = m.Download(ctx, catalog.PLaMo, "linux", nil); !errors.Is(err, sentinel) || hits != 1 {
		t.Fatalf("saved consent: %v, hits %d", err, hits)
	}
	assertClean(t, m)
}

func TestPublicAPINoImplicitNetwork(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network request"); return nil, nil })
	if got, err := m.Directory(catalog.Silero, "linux"); err != nil || got != filepath.Join(dir, "downloads", catalog.Silero) {
		t.Fatalf("directory: %q, %v", got, err)
	}
	if ok, err := m.CheckInstalled(context.Background(), catalog.Silero, "linux"); ok || err != nil {
		t.Fatalf("missing: %v, %v", ok, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read-only check created directory")
	}
	if _, err = New(""); err == nil {
		t.Fatal("empty directory accepted")
	}
	if err = m.Download(context.Background(), "../escape", "linux", nil); err == nil {
		t.Fatal("unknown ID accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = m.Download(ctx, catalog.Silero, "linux", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	m.turn <- struct{}{}
	if err = m.Download(ctx, catalog.Silero, "linux", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
	<-m.turn
}

func TestPublishRollback(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture", "model"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = publish(root, "nonexistent-stage", "fixture"); err == nil {
		t.Fatal("rename succeeded unexpectedly")
	}
	b, err := root.ReadFile("fixture/model")
	if err != nil || string(b) != "old" {
		t.Fatalf("rollback lost old file: %q %v", b, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backup not restored: %v %v", entries, err)
	}
}

func TestDownloadSymlinkEscape(t *testing.T) {
	body := []byte("bytes")
	m, s := testManager(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(m.dataDir, "downloads")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	a := fixture(body)
	a.Sources[0].URL = s.URL
	if err := m.install(context.Background(), a, nil); err == nil {
		t.Fatal("followed downloads symlink outside data directory")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside data directory: %v %v", entries, err)
	}
}

func TestCanceledHash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyCopy(ctx, nil, strings.NewReader("x"), 1, digest([]byte("x"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestDefaultTransportKeepsProxy(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := m.client.Transport.(*http.Transport)
	if tr.Proxy == nil || reflect.ValueOf(tr.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("default environment proxy policy lost")
	}
}

func ExampleManager_Directory() {
	m, _ := New("data")
	dir, _ := m.Directory(catalog.Silero, "linux")
	fmt.Println(filepath.Base(dir))
	// Output: silero-vad-v6.2.3
}

func TestNetworkErrorHasNoDirectoryHint(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Socket permission errors also cannot be fixed by changing the data directory.
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrPermission}
	})
	a := fixture([]byte("bytes"))
	a.Sources[0].URL = "https://example.invalid/model"
	err = m.install(context.Background(), a, nil)
	if errcode.Of(err) != errcode.DownloadHTTP || strings.Contains(err.Error(), "-data-dir") {
		t.Fatalf("network error: %v", err)
	}
	assertClean(t, m)
}
