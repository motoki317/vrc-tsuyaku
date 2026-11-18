package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var catalogKeyPattern = regexp.MustCompile(`['"]((?:label|action|state|value|title|error|note|instance)\.[A-Za-z0-9_.]*)['"]`)

// The page has no tooltips, so nothing sets a title attribute or property. The document's <title> element stays.
var titlePattern = regexp.MustCompile(`<(?:[^<>]|\{[^}]*\})*\stitle\s*=|\.title\s*=|[{,]\s*title\s*:|['"]title['"]`)

// A browser lets Exit's window.close() close the tab only while the tab's history holds one entry, so the page
// changes screens and URLs without adding entries.
var historyEntryPattern = regexp.MustCompile(`pushState|location\.assign|location\.(hash|href|search|pathname)\s*=[^=]|location\s*=[^=]`)

func TestScriptSources(t *testing.T) {
	en := loadCatalog(t, "en")
	err := fs.WalkDir(os.DirFS("."), "src", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (filepath.Ext(name) != ".js" && filepath.Ext(name) != ".jsx") {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		script := string(data)
		if m := titlePattern.FindString(script); m != "" {
			t.Errorf("%s sets a title with %q, which shows a tooltip", name, m)
		}
		if m := historyEntryPattern.FindString(script); m != "" {
			t.Errorf("%s adds a history entry with %q, so Exit could not close the tab", name, m)
		}
		for _, m := range catalogKeyPattern.FindAllStringSubmatch(script, -1) {
			// errorText completes this key with the server's code.
			if _, ok := en[m[1]]; !ok && m[1] != "error.code." {
				t.Errorf("%s names %q, which the catalogs lack", name, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
