package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The page explains itself through layout and state, so the catalogs hold no hint or description category.
// A string that a usability test justifies goes in "note", with a "//note.NAME" key that names the finding.
var stringBudgets = map[string]int{
	"label":  40,
	"action": 48,
	"state":  48,
	"value":  60,
	"title":  64,
	"error":  120,
	"note":   120,
}

var catalogKeyPattern = regexp.MustCompile(`'((?:label|action|state|value|title|error|note)\.[A-Za-z0-9_.]*)'`)

var placeholderPattern = regexp.MustCompile(`\{\w+\}`)

func placeholders(text string) []string {
	return slices.Sorted(slices.Values(placeholderPattern.FindAllString(text, -1)))
}

func loadCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	data, err := ui.ReadFile("ui/i18n/" + lang + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]string
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("%s.json: %v", lang, err)
	}
	return catalog
}

func TestStringCatalogs(t *testing.T) {
	en, ja := loadCatalog(t, "en"), loadCatalog(t, "ja")
	for key, text := range en {
		other, ok := ja[key]
		if !ok {
			t.Errorf("ja.json lacks %q", key)
		} else if !slices.Equal(placeholders(text), placeholders(other)) {
			// The script fills placeholders by name, and error.microphones puts an icon at {refresh}.
			t.Errorf("%q has placeholders %v in en.json but %v in ja.json", key, placeholders(text), placeholders(other))
		}
	}
	for key := range ja {
		if _, ok := en[key]; !ok {
			t.Errorf("en.json lacks %q", key)
		}
	}
	for lang, catalog := range map[string]map[string]string{"en": en, "ja": ja} {
		for key, text := range catalog {
			if note, ok := strings.CutPrefix(key, "//"); ok {
				if _, ok := catalog[note]; !ok || !strings.HasPrefix(note, "note.") {
					t.Errorf("%s.json: comment %q names no note key", lang, key)
				}
				continue
			}
			category, _, _ := strings.Cut(key, ".")
			budget, ok := stringBudgets[category]
			if !ok {
				t.Errorf("%s.json: %q has category %q, want one of label, action, state, value, title, error, note", lang, key, category)
				continue
			}
			if category == "note" {
				if _, ok := catalog["//"+key]; !ok {
					t.Errorf("%s.json: %q needs a %q comment that names its usability finding", lang, key, "//"+key)
				}
			}
			if n := utf8.RuneCountInString(text); n > budget {
				t.Errorf("%s.json: %q has %d characters, over the %s budget of %d", lang, key, n, category, budget)
			}
		}
	}
}

// Every visible string and accessible name in index.html comes from the catalogs.
func TestIndexHTMLHasNoText(t *testing.T) {
	data, err := ui.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := regexp.MustCompile(`(?s)<!--.*?-->|<script\b.*?</script>|<style\b.*?</style>`).ReplaceAllString(string(data), "")
	tags := regexp.MustCompile(`<[^>]*>`)
	for _, text := range tags.Split(page, -1) {
		if text := strings.TrimSpace(html.UnescapeString(text)); text != "" {
			t.Errorf("index.html shows %q outside the catalogs", text)
		}
	}
	literal := regexp.MustCompile(`\s(aria-label|placeholder|alt|label)\s*=`)
	en := loadCatalog(t, "en")
	keyAttribute := regexp.MustCompile(`\sdata-i18n(?:-[a-z-]+)?="([^"]*)"`)
	for _, tag := range tags.FindAllString(page, -1) {
		if m := literal.FindStringSubmatch(tag); m != nil {
			t.Errorf("index.html sets %s literally in %s; use a data-i18n attribute", m[1], tag)
		}
		for _, m := range keyAttribute.FindAllStringSubmatch(tag, -1) {
			if _, ok := en[m[1]]; !ok {
				t.Errorf("index.html names %q, which the catalogs lack", m[1])
			}
		}
	}
}

// The page has no tooltips, so nothing sets a title attribute or property. The document's <title> element stays.
var titlePattern = regexp.MustCompile(`<[^<>]*\stitle\s*=|\.title\s*=|[{,]\s*title\s*:|['"]title['"]`)

func TestNoTitleAttribute(t *testing.T) {
	for _, name := range []string{"ui/index.html", "ui/app.js"} {
		data, err := ui.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if m := titlePattern.FindString(string(data)); m != "" {
			t.Errorf("%s sets a title with %q, which shows a tooltip", name, m)
		}
	}
}

// A browser lets Exit's window.close() close the tab only while the tab's history holds one entry, so the page
// changes screens and URLs without adding entries.
var historyEntryPattern = regexp.MustCompile(`pushState|location\.assign|location\.(hash|href|search|pathname)\s*=[^=]|location\s*=[^=]`)

func TestScreenChangesAddNoHistoryEntry(t *testing.T) {
	data, err := ui.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if m := historyEntryPattern.FindString(string(data)); m != "" {
		t.Errorf("app.js adds a history entry with %q, so Exit could not close the tab", m)
	}
}

func TestAppScriptStrings(t *testing.T) {
	data, err := ui.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	en := loadCatalog(t, "en")
	for _, m := range catalogKeyPattern.FindAllStringSubmatch(script, -1) {
		// errorText completes this key with the server's code, and the loop below checks every code.
		if m[1] == "error.code." {
			continue
		}
		if _, ok := en[m[1]]; !ok {
			t.Errorf("app.js names %q, which the catalogs lack", m[1])
		}
	}
	for _, code := range errorCodes(t) {
		if code == "unknown" {
			continue
		}
		if _, ok := en["error.code."+code]; !ok {
			t.Errorf("the catalogs lack error.code.%s", code)
		}
	}
}

// errorCodes reads the codes from the errcode source, so a new code fails this test until the catalogs name it.
func errorCodes(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "internal", "errcode", "errcode.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			for _, value := range spec.(*ast.ValueSpec).Values {
				if lit, ok := value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					code, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					codes = append(codes, code)
				}
			}
		}
	}
	if len(codes) == 0 {
		t.Fatal("errcode.go declares no codes")
	}
	return codes
}
