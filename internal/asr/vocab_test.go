package asr

import (
	"strings"
	"testing"
)

func TestVocabularyDecode(t *testing.T) {
	v := vocabulary{"<unk>", "▁こんにちは", "▁world", "<0xE3>", "<0x81>", "<0x82>", "<0xFF>", "<", "0x41", ">", "<0xF0>", "<0x9F>", "<0x98>", "<0x80>"}
	for _, tc := range []struct {
		name string
		ids  []int
		want string
	}{
		{"empty", nil, ""},
		{"spaces", []int{1, 2}, "こんにちは world"},
		{"unknown", []int{1, 0, 2}, "こんにちは ⁇  world"},
		{"unknown alone", []int{0}, "⁇"},
		{"byte fallback", []int{3, 4, 5}, "あ"},
		{"truncated bytes", []int{3, 4}, "��"},
		{"valid prefix invalid suffix", []int{3, 4, 4, 5}, "ぁ�"},
		{"invalid between text", []int{1, 6, 2}, "こんにちは� world"},
		{"four bytes", []int{10, 11, 12, 13}, "😀"},
		{"literal byte spelling", []int{7, 8, 9}, "<0x41>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := v.decode(tc.ids)
			if err != nil || got != tc.want {
				t.Fatalf("decode = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
	for _, id := range []int{-1, len(v)} {
		if _, err := v.decode([]int{id}); err == nil {
			t.Fatalf("accepted invalid token %d", id)
		}
	}
	got, err := (vocabulary{"<0x1C>", "<0x1D>", "<0x1E>", "<0x1F>", "text"}).decode([]int{0, 1, 4, 2, 3})
	if err != nil || got != "text" {
		t.Fatalf("Python boundary whitespace: %q, %v", got, err)
	}
}

func TestReadVocabulary(t *testing.T) {
	v, err := readVocabulary(strings.NewReader("▁world 1\nhello 0\n<blk> 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.decode([]int{0, 1})
	if err != nil || got != "hello world" {
		t.Fatalf("decode = %q, %v", got, err)
	}
	for _, text := range []string{"", "no separator", " 0\n", "a -1\n", "a 1\n", "a 0\nb 0\n", "a b 0\n", "a 9999999999\n", "\xff 0\n", "a 0\n\n"} {
		if _, err := readVocabulary(strings.NewReader(text)); err == nil {
			t.Errorf("accepted malformed vocabulary %q", text)
		}
	}
}
