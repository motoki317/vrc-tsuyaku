package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"
)

//go:embed notices
var notices embed.FS

func noticesDocument() (string, error) {
	var document strings.Builder
	err := fs.WalkDir(notices, "notices", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == "SOURCE" {
			return err
		}
		dir := path.Dir(name)
		var component, version, source string
		switch {
		case dir == "notices/go":
			component, version, source = "Go", runtime.Version(), "https://go.dev/LICENSE"
		case strings.HasPrefix(dir, "notices/modules/"):
			identity := strings.TrimPrefix(dir, "notices/modules/")
			component, version, _ = strings.Cut(identity, "@")
			source = "https://pkg.go.dev/" + identity
		case strings.HasPrefix(dir, "notices/other/"):
			component, version, _ = strings.Cut(strings.TrimPrefix(dir, "notices/other/"), "@")
			data, err := notices.ReadFile(dir + "/SOURCE")
			if err != nil {
				return err
			}
			source = strings.TrimSpace(string(data))
		}
		if component == "" || version == "" || source == "" {
			return fmt.Errorf("missing notice metadata: %s", name)
		}
		text, err := notices.ReadFile(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(&document, "Component: %s\nVersion: %s\nSource: %s\nFile: %s\n\n", component, version, source, entry.Name())
		document.Write(text)
		document.WriteString("\n\n")
		return nil
	})
	return document.String(), err
}

func serveNotices(w http.ResponseWriter, r *http.Request) {
	document, err := noticesDocument()
	if err != nil {
		http.Error(w, "cannot read third-party notices", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeContent(w, r, "notices.txt", time.Time{}, strings.NewReader(document))
}
