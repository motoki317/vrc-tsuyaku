package chatbox

import (
	"bytes"
	"testing"
)

func TestEncodePage(t *testing.T) {
	const prefix = "/chatbox/input\x00\x00,sTF\x00\x00\x00\x00"
	for _, tc := range []struct{ text, payload string }{
		{"", "\x00\x00\x00\x00"},
		{"a", "a\x00\x00\x00"},
		{"ab", "ab\x00\x00"},
		{"abc", "abc\x00"},
		{"abcd", "abcd\x00\x00\x00\x00"},
		{"猫", "\xe7\x8c\xab\x00"},
		{"😀\nA", "\xf0\x9f\x98\x80\nA\x00\x00"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got, want := encodePage(tc.text), []byte(prefix+tc.payload); !bytes.Equal(got, want) {
				t.Fatalf("packet = % x, want % x", got, want)
			}
		})
	}
}
