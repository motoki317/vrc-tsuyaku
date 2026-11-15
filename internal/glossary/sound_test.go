package glossary

import "testing"

func TestSoundKey(t *testing.T) {
	for _, tc := range []struct{ word, want string }{
		{"Selestia", "selestia"},
		{"Celestia", "selestia"},
		{"Cosmoria", "kosmoria"},
		{"Kosmoria", "kosmoria"},
		{"Poiyomi", "poiomi"},
		{"Poyomi", "poiomi"},
		{"FUJIYAMA", "fujiama"},
		{"Fujiama", "fujiama"},
		{"cecyci", "sesisi"},
		{"cacao", "kakao"},
		{"phone", "fone"},
		{"back", "bak"},
		{"lazy", "lasi"},
		{"beet", "bit"},
		{"beat", "bit"},
		{"boot", "but"},
		{"bout", "but"},
		{"balloon", "balun"},
		{"cce", "kse"},
		{"eea", "ia"},
		{"eeea", "i"},
		{"eey", "i"},
		{"C'e'l'e's't'i'a", "selestia"},
		{"Celestia's", "selestias"},
		{"Celestias", "selestias"},
		{"'", ""},
		{"", ""},
	} {
		t.Run(tc.word, func(t *testing.T) {
			if got := soundKey(tc.word); got != tc.want {
				t.Fatalf("sound key = %q, want %q", got, tc.want)
			}
		})
	}
}
