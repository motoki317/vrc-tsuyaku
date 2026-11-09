package chatbox

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPages(t *testing.T) {
	for _, tc := range []struct {
		name, original, translation string
		want                        []string
	}{
		{"order", "こんにちは。", "Hello.", []string{"こんにちは。\nHello."}},
		{"empty", "", "", []string{}},
		{"whitespace", " \t\n", "\n ", []string{}},
		{"original only", "hello", "", []string{"hello"}},
		{"translation only", "", "hello", []string{"hello"}},
		{"interior whitespace", "a  b\nc", "d", []string{"a  b\nc\nd"}},
		{"exact runes", strings.Repeat("猫", 142), "😀", []string{strings.Repeat("猫", 142) + "\n😀"}},
		{"rune cut", strings.Repeat("😀", 145), "", []string{strings.Repeat("😀", 144), "😀"}},
		{"nine lines", strings.Repeat("a\n", 8) + "a", "", []string{strings.Repeat("a\n", 8) + "a"}},
		{"ten lines", strings.Repeat("a\n", 9) + "a", "", []string{strings.Repeat("a\n", 8) + "a", "a"}},
		{"sentence before space", "First sentence. " + strings.Repeat("word ", 30), "", []string{"First sentence.", strings.TrimSpace(strings.Repeat("word ", 29)), "word"}},
		{"last sentence", "One. Two! " + strings.Repeat("x", 140), "", []string{"One. Two!", strings.Repeat("x", 140)}},
		{"Japanese sentence", "こんにちは。" + strings.Repeat("猫", 140), "", []string{"こんにちは。", strings.Repeat("猫", 140)}},
		{"word break", strings.Repeat("word ", 30), "", []string{strings.TrimSpace(strings.Repeat("word ", 29)), "word"}},
		{"decimal", "3.14" + strings.Repeat("x", 142), "", []string{"3.14" + strings.Repeat("x", 140), "xx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pages(tc.original + "\n" + tc.translation)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pages = %q, want %q", got, tc.want)
			}
			for _, page := range got {
				if !utf8.ValidString(page) || utf8.RuneCountInString(page) > 144 || strings.Count(page, "\n")+1 > 9 || strings.TrimSpace(page) == "" {
					t.Fatalf("invalid page: %q", page)
				}
			}
		})
	}
}

func FuzzPages(f *testing.F) {
	for _, s := range []string{"", "hello world", "こんにちは。\nHello.", strings.Repeat("😀", 145), strings.Repeat("a\n", 20)} {
		f.Add(s, "translation")
	}
	f.Fuzz(func(t *testing.T, original, translation string) {
		if !utf8.ValidString(original) || !utf8.ValidString(translation) {
			t.Skip()
		}
		got := pages(original + "\n" + translation)
		var joined strings.Builder
		for _, page := range got {
			if !utf8.ValidString(page) || utf8.RuneCountInString(page) > 144 || strings.Count(page, "\n")+1 > 9 || strings.TrimSpace(page) == "" {
				t.Fatalf("invalid page: %q", page)
			}
			joined.WriteString(page)
		}
		// Paging may consume separator whitespace, but must keep every other rune.
		if strings.Join(strings.Fields(joined.String()), "") != strings.Join(strings.Fields(original+translation), "") {
			t.Fatalf("paging lost or reordered text: %q", got)
		}
	})
}
