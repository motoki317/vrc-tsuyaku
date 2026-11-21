package web

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The catalogs omit hint and description categories. instance.description is the
// exception. The waiting modal must explain why the app is waiting.
// A string that a usability test justifies goes in "note", with a "//note.NAME" key that names the finding.
var stringBudgets = map[string]int{
	"label":                40,
	"action":               48,
	"state":                48,
	"value":                60,
	"title":                64,
	"error":                120,
	"note":                 120,
	"instance":             60,
	"instance.description": 140,
}

var placeholderPattern = regexp.MustCompile(`\{\w+\}`)

func placeholders(text string) []string {
	return slices.Sorted(slices.Values(placeholderPattern.FindAllString(text, -1)))
}

func loadCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	data, err := os.ReadFile("i18n/" + lang + ".json")
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
			budget, ok := stringBudgets[key]
			if !ok {
				budget, ok = stringBudgets[category]
			}
			if !ok {
				t.Errorf("%s.json: %q has category %q, want one of label, action, state, value, title, error, note, instance", lang, key, category)
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

func TestErrorCodesHaveStrings(t *testing.T) {
	en := loadCatalog(t, "en")
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
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "internal", "errcode", "errcode.go"), nil, 0)
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
