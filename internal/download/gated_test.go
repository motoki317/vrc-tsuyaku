package download

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/asr"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

func TestPinnedLinuxDownloads(t *testing.T) {
	base := os.Getenv("VRC_TSUYAKU_TEST_DOWNLOAD_DIR")
	if base == "" {
		t.Skip("set VRC_TSUYAKU_TEST_DOWNLOAD_DIR to enable network downloads")
	}
	if runtime.GOOS != "linux" {
		t.Skip("Linux release artifacts")
	}
	dir, err := os.MkdirTemp(base, "download-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, id := range []string{catalog.Silero, catalog.LlamaCPP, catalog.ONNXRuntime} {
		if err = m.Download(ctx, id, "linux", nil); err != nil {
			t.Fatal(err)
		}
		if ok, err := m.CheckInstalled(ctx, id, "linux"); !ok || err != nil {
			t.Fatalf("%s: installed %v: %v", id, ok, err)
		}
		t.Logf("%s: downloaded and verified every installed file", id)
	}
	// Exercise the selected libraries, including dependencies loaded beside the server.
	llamaDir, _ := m.Directory(catalog.LlamaCPP, "linux")
	cmd := exec.CommandContext(ctx, catalog.LlamaServer(llamaDir), "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("server: %v\n%s", err, output)
	}
	t.Logf("server: %s", output)
	ortDir, _ := m.Directory(catalog.ONNXRuntime, "linux")
	engine, err := asr.NewEngine(catalog.ORTLibrary(ortDir))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	sileroDir, _ := m.Directory(catalog.Silero, "linux")
	vad, release, err := engine.LoadSilero(catalog.SileroModel(sileroDir))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	probability, err := vad.NewStream().Probability(ctx, make([]float32, 512))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("downloaded ORT and Silero: silent-window probability %g", probability)
}
