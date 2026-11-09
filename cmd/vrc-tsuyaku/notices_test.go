package main

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestModuleNoticesMatchGoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mods, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool)
	for _, req := range mods.Require {
		want[req.Mod.String()] = true
	}
	err = fs.WalkDir(notices, "notices/modules", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || !strings.Contains(entry.Name(), "@") {
			return err
		}
		identity := strings.TrimPrefix(name, "notices/modules/")
		if !want[identity] {
			t.Errorf("embedded module %s is absent from go.mod; run just notices", identity)
		}
		delete(want, identity)
		files, err := notices.ReadDir(name)
		if err != nil {
			return err
		}
		licensed := false
		for _, file := range files {
			if strings.HasPrefix(strings.ToUpper(file.Name()), "LICEN") {
				text, err := notices.ReadFile(name + "/" + file.Name())
				if err != nil || len(text) == 0 {
					t.Errorf("empty or unreadable module license %s/%s: %v", identity, file.Name(), err)
				}
				licensed = licensed || len(text) > 0
			}
		}
		if !licensed {
			t.Errorf("no license for %s; run just notices", identity)
		}
		return fs.SkipDir
	})
	if err != nil {
		t.Fatal(err)
	}
	for identity := range want {
		t.Errorf("no embedded license for %s; run just notices", identity)
	}
}

func TestNoticesHTTP(t *testing.T) {
	a := testApp(t)
	w := request(a, "GET", "/notices.txt", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("notices response: %d, %s", w.Code, w.Header().Get("Content-Type"))
	}
	checkSecurityHeaders(t, w)
	if !strings.Contains(w.Body.String(), "Component: Go\nVersion: "+runtime.Version()+"\nSource: https://go.dev/LICENSE\n") {
		t.Fatal("Go license lacks its component, version or source")
	}
	count := 0
	err := fs.WalkDir(notices, "notices", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		text, err := notices.ReadFile(name)
		if err != nil {
			return err
		}
		if entry.Name() == "SOURCE" {
			if !strings.Contains(w.Body.String(), "Source: "+strings.TrimSpace(string(text))+"\n") {
				t.Errorf("response lacks source from %s", name)
			}
			return nil
		}
		count++
		if len(text) == 0 || !strings.Contains(w.Body.String(), "\n\n"+string(text)+"\n\n") {
			t.Errorf("response lacks verbatim %s", name)
		}
		if identity, ok := strings.CutPrefix(path.Dir(name), "notices/modules/"); ok {
			component, version, _ := strings.Cut(identity, "@")
			if !strings.Contains(w.Body.String(), "Component: "+component+"\nVersion: "+version+"\nSource: https://pkg.go.dev/"+identity+"\n") {
				t.Errorf("response lacks module metadata for %s", identity)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(w.Body.String(), "Component: "); got != count {
		t.Fatalf("response has %d headers for %d texts", got, count)
	}
	if head := request(a, "HEAD", "/notices.txt", ""); head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatal("HEAD did not serve notices without a body", head.Code)
	}
	if post := request(a, "POST", "/notices.txt", ""); post.Code < http.StatusBadRequest {
		t.Fatal("notices accepted POST", post.Code)
	}
	page, err := ui.ReadFile("ui/index.html")
	if err != nil || !strings.Contains(string(page), `href="/notices.txt"`) {
		t.Fatal("About does not link to embedded notices", err)
	}
}
