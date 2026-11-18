package web

import (
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

func TestBundledNotices(t *testing.T) {
	data, err := fs.ReadFile(Files, "licenses.json")
	if err != nil {
		t.Fatal(err)
	}
	var packages []struct{ Name, Version string }
	if err := json.Unmarshal(data, &packages); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool)
	for _, pkg := range packages {
		want[pkg.Name+"@"+pkg.Version] = true
	}

	data, err = os.ReadFile("package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct{ Name string }
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	locked := make(map[string]bool)
	for location, pkg := range lock.Packages {
		parts := strings.Split("/"+location, "/node_modules/")
		if len(parts) > 1 {
			locked[cmp.Or(pkg.Name, parts[len(parts)-1])] = true
		}
	}

	other := os.DirFS("../cmd/vrc-tsuyaku/notices/other")
	err = fs.WalkDir(other, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Name() != "SOURCE" {
			return err
		}
		id := path.Dir(name)
		if want[id] {
			text, err := fs.ReadFile(other, id+"/LICENSE")
			if err != nil || len(text) == 0 {
				t.Errorf("%s has no readable license", id)
			}
			delete(want, id)
			return nil
		}
		if at := strings.LastIndexByte(id, '@'); at >= 0 && locked[id[:at]] {
			t.Errorf("notice %s has no bundled package", id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for id := range want {
		t.Errorf("bundled package %s has no notice", id)
	}
}
