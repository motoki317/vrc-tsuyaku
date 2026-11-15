package glossary

import (
	"reflect"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		term             Term
		match            bool
	}{
		{"reading", "ききょうとキキョウ", "桔梗と桔梗", Term{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, false},
		{"canonical", "桔梗と桔梗", "桔梗と桔梗", Term{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, true},
		{"case", "LILTOONを入れた", "lilToonを入れた", Term{Source: "lilToon", Target: "lilToon"}, true},
		{"particle", "vrcfuryを入れた", "VRCFuryを入れた", Term{Source: "VRCFury", Target: "VRCFury"}, true},
		{"after long vowel", "ユーザーliltoon", "ユーザーlilToon", Term{Source: "lilToon", Target: "lilToon"}, true},
		{"inside word", "sublime", "sublime", Term{Source: "Lime", Target: "ライム"}, false},
		{"suffix", "Limes", "Limes", Term{Source: "Lime", Target: "ライム"}, false},
		{"digit prefix", "2Lime", "2Lime", Term{Source: "Lime", Target: "ライム"}, false},
		{"digit suffix", "Lime2", "Lime2", Term{Source: "Lime", Target: "ライム"}, false},
		{"punctuation", "(lime), lime!", "(Lime), Lime!", Term{Source: "Lime", Target: "ライム"}, true},
		{"different spaced scripts", "Жlimeα", "Жlimeα", Term{Source: "Lime", Target: "ライム"}, false},
		{"hangul", "한lime", "한lime", Term{Source: "Lime", Target: "ライム"}, false},
		{"nonspace scripts", "林limeไทย", "林Limeไทย", Term{Source: "Lime", Target: "ライム"}, true},
		{"kana fold", "まぬかって", "マヌカって", Term{Source: "マヌカ", Target: "Manuka"}, true},
		{"katakana reading suffix", "リンドの衣装", "リンドの衣装", Term{Source: "凛", Reading: "りん", Target: "Rin"}, false},
		{"katakana source suffix", "リンドの衣装", "リンドの衣装", Term{Source: "リン", Target: "Rin"}, false},
		{"katakana prefix", "プリンの衣装", "プリンの衣装", Term{Source: "リン", Target: "Rin"}, false},
		{"katakana long vowel prefix", "リーリン", "リーリン", Term{Source: "リン", Target: "Rin"}, false},
		{"katakana long vowel end", "リード", "リード", Term{Source: "リー", Target: "Rii"}, false},
		{"short hiragana reading inside word", "ちゃんとわかる", "ちゃんとわかる", Term{Source: "永遠", Reading: "とわ", Target: "Towa"}, false},
		{"short katakana source inside hiragana", "いまやってる", "いまやってる", Term{Source: "マヤ"}, false},
		{"short katakana source inside hiragana word", "ちゃんとわかるよ", "ちゃんとわかるよ", Term{Source: "トワ"}, false},
		{"short reading with long vowel", "まーね", "まーね", Term{Source: "真亜", Reading: "まー", Target: "Maa"}, false},
		{"short source with long vowel", "まーね", "まーね", Term{Source: "マー"}, false},
		{"short hiragana source with long vowel", "まーね", "まーね", Term{Source: "まー"}, true},
		{"short katakana reading", "トワ", "永遠", Term{Source: "永遠", Reading: "とわ", Target: "Towa"}, false},
		{"short mixed kana reading", "とワ", "永遠", Term{Source: "永遠", Reading: "とわ", Target: "Towa"}, false},
		{"short hiragana reading before particle", "まやの衣装", "まやの衣装", Term{Source: "舞夜", Reading: "まや", Target: "Maya"}, false},
		{"short katakana reading before particle", "マヤの衣装", "舞夜の衣装", Term{Source: "舞夜", Reading: "まや", Target: "Maya"}, false},
		{"single hiragana reading", "か", "か", Term{Source: "華", Reading: "か", Target: "Ka"}, false},
		{"single katakana reading", "カ", "華", Term{Source: "華", Reading: "か", Target: "Ka"}, false},
		{"three character hiragana reading", "すずやの衣装", "紗夜の衣装", Term{Source: "紗夜", Reading: "すずや", Target: "Suzuya"}, false},
		{"short hiragana source", "しおの衣装", "しおの衣装", Term{Source: "しお", Reading: "しお", Target: "Shio"}, true},
		{"short source folded", "シオの衣装", "しおの衣装", Term{Source: "しお", Reading: "しお", Target: "Shio"}, true},
		{"mixed kana reading", "スズや", "紗夜", Term{Source: "紗夜", Reading: "すずや", Target: "Suzuya"}, false},
		{"katakana particle", "リンの衣装", "凛の衣装", Term{Source: "凛", Reading: "りん", Target: "Rin"}, false},
		{"katakana small tsu", "マヌカッテ", "マヌカッテ", Term{Source: "マヌカ", Target: "Manuka"}, false},
		{"long vowel", "ハッカー", "ハッカー", Term{Source: "薄荷", Reading: "はっか", Target: "Hakka"}, false},
		{"canonical long vowel", "ハッカー", "ハッカー", Term{Source: "ハッカ", Target: "Hakka"}, false},
		{"small kana", "キキョウゃ", "キキョウゃ", Term{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, false},
		{"long source", "リルトゥーンー", "リルトゥーンー", Term{Source: "lilToon", Reading: "りるとぅーん", Target: "lilToon"}, false},
		{"reading before digit", "リルトゥーン2を入れた", "lilToon2を入れた", Term{Source: "lilToon", Reading: "りるとぅーん", Target: "lilToon"}, false},
		{"small tsu", "ききょうっ", "桔梗っ", Term{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, false},
		{"partial reading", "ききょう", "ききょう", Term{Source: "菊", Reading: "きく", Target: "Kiku"}, false},
		{"multibyte offsets", "😀K ききょう。", "😀K K。", Term{Source: "K", Reading: "ききょう", Target: "Kay"}, true},
		{"empty", "", "", Term{Source: "Lime", Target: "ライム"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terms := []Term{tc.term}
			got, used := Rewrite(tc.text, terms)
			if got != tc.want {
				t.Fatalf("rewrite = %q, want %q", got, tc.want)
			}
			present := tc.match || tc.text != tc.want
			if (len(used) == 1) != present || len(used) > 1 || (len(used) == 1 && used[0] != tc.term) {
				t.Fatalf("terms = %+v, want present %v", used, present)
			}
		})
	}
	for _, next := range "ーゃゅょぁぃぅぇぉャュョァィゥェォ" {
		text := "マヌカ" + string(next)
		term := []Term{{Source: "マヌカ", Target: "Manuka"}}
		if got, used := Rewrite(text, term); got != text || len(used) != 0 {
			t.Errorf("matched before %c", next)
		}
	}
}

func TestRewritePrecedence(t *testing.T) {
	for _, tc := range []struct {
		text, want string
		terms      []Term
	}{
		{"ききょう", "桔梗", []Term{{Source: "木", Reading: "き"}, {Source: "桔梗", Reading: "ききょう"}}},
		{"まぬか", "マヌカ", []Term{{Source: "マヌカ"}, {Source: "別", Reading: "まぬか"}}},
		{"まぬか", "別", []Term{{Source: "別", Reading: "まぬか"}, {Source: "マヌカ"}}},
		{"マヤ", "舞夜", []Term{{Source: "舞夜", Reading: "まや"}, {Source: "別", Reading: "舞夜"}}},
		{"ききょうまぬか", "桔梗マヌカ", []Term{{Source: "桔梗", Reading: "ききょう"}, {Source: "マヌカ"}}},
		{"ab cd ef", "First ef", []Term{{Source: "First", Reading: "ab cd"}, {Source: "Second", Reading: "cd ef"}}},
		{"😀 Unchanged", "😀 Unchanged", nil},
	} {
		if got, _ := Rewrite(tc.text, tc.terms); got != tc.want {
			t.Errorf("rewrite %q = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestRewriteSoundKeys(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		words            []string
		used             []int
	}{
		{"name", "Fujiama", "FUJIYAMA", []string{"FUJIYAMA"}, []int{0}},
		{"case and repeats", "fujiama FUJIAMA Fujiama", "FUJIYAMA FUJIYAMA FUJIYAMA", []string{"FUJIYAMA"}, []int{0}},
		{"soft c", "Celestia", "Selestia", []string{"Selestia"}, []int{0}},
		{"hard c", "Cosmoria", "Kosmoria", []string{"Kosmoria"}, []int{0}},
		{"y and doubles", "Poiyomi", "Poyomi", []string{"Poyomi"}, []int{0}},
		{"internal apostrophe", "Fu'jiama", "FUJIYAMA", []string{"FUJIYAMA"}, []int{0}},
		{"surrounding apostrophes", "'Fujiama'", "FUJIYAMA", []string{"FUJIYAMA"}, []int{0}},
		{"possessive", "Celestia's", "Celestia's", []string{"Selestia"}, nil},
		{"plural", "Celestias", "Celestias", []string{"Selestia"}, nil},
		{"whole apostrophe run", "Celestia's", "Selestias", []string{"Selestias"}, []int{0}},
		{"no edit distance", "Fujima", "Fujima", []string{"FUJIYAMA"}, nil},
		{"vowels stay distinct", "Sulestia", "Sulestia", []string{"Selestia"}, nil},
		{"maximal run", "XFujiama", "XFujiama", []string{"FUJIYAMA"}, nil},
		{"digits delimit runs", "2Fujiama3", "2FUJIYAMA3", []string{"FUJIYAMA"}, []int{0}},
		{"non ASCII delimiters", "éFujiamaあ", "éFUJIYAMAあ", []string{"FUJIYAMA"}, []int{0}},
		{"non ASCII word", "Fujíama", "Fujíama", []string{"FUJIYAMA"}, nil},
		{"hyphen splits runs", "Fuji-ama", "Fuji-ama", []string{"FUJIYAMA"}, nil},
		{"no multiword match", "Fuji ama", "Fuji ama", []string{"FUJIYAMA"}, nil},
		{"multiword entry", "Fujiama", "Fujiama", []string{"Fuji yama"}, nil},
		{"multiword exact", "fuji yama", "Fuji yama", []string{"Fuji yama"}, []int{0}},
		{"short key", "Lyme", "Lyme", []string{"Lime"}, nil},
		{"short collapsed key", "Zii", "Zii", []string{"Ziiii"}, nil},
		{"entry punctuation", "Fujiama", "Fujiama", []string{"Fuji-ama"}, nil},
		{"entry digit", "Fujiama", "Fujiama", []string{"FUJIYAMA2"}, nil},
		{"exact second entry", "fujiama", "Fujiama", []string{"FUJIYAMA", "Fujiama"}, []int{1}},
		{"exact reverse order", "fujiyama", "FUJIYAMA", []string{"Fujiama", "FUJIYAMA"}, []int{1}},
		{"exact word blocked by digits", "2Fujiama3", "2Fujiama3", []string{"FUJIYAMA", "Fujiama"}, nil},
		{"short exact prefix", "Fuji'ama", "FUJIYAMA", []string{"Fuji", "FUJIYAMA"}, []int{1}},
		{"longer sound candidate", "Celestia's", "Selestias", []string{"Celestia", "Selestias"}, []int{1}},
		{"first key wins", "Cellestia", "Selestia", []string{"Selestia", "Celestia"}, []int{0}},
		{"first key reverse order", "Cellestia", "Celestia", []string{"Celestia", "Selestia"}, []int{0}},
		{"exact phrase takes priority", "celestia outfit", "Celestia outfit", []string{"Selestia", "Celestia outfit"}, []int{1}},
		{"term order", "Celestia Fujiama Celestia", "Selestia FUJIYAMA Selestia", []string{"FUJIYAMA", "Selestia"}, []int{0, 1}},
		{"empty", "", "", []string{"FUJIYAMA"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entries []Entry
			for _, word := range tc.words {
				entries = append(entries, Entry{SourceLang: "en", Source: word, TargetLang: "ja", Target: "target for " + word})
			}
			terms := Terms(entries, "en", "ja")
			var wantUsed []Term
			for _, index := range tc.used {
				wantUsed = append(wantUsed, terms[index])
			}
			got, used := Rewrite(tc.text, terms)
			if got != tc.want || !reflect.DeepEqual(used, wantUsed) {
				t.Fatalf("rewrite = %q, %+v, want %q, %+v", got, used, tc.want, wantUsed)
			}
		})
	}
}

func TestRewriteSoundKeyLanguages(t *testing.T) {
	for _, source := range []string{"en", "ja", "fr", "auto", "pt"} {
		t.Run(source, func(t *testing.T) {
			entrySource := source
			if source == "auto" || source == "pt" {
				entrySource = "en"
			}
			terms := Terms([]Entry{{SourceLang: entrySource, Source: "FUJIYAMA", TargetLang: "de", Target: "target"}}, source, "de")
			if len(terms) != 1 {
				t.Fatalf("terms = %+v, want one term", terms)
			}
			want, matches := "Fujiama", 0
			if source == "en" {
				want, matches = "FUJIYAMA", 1
			}
			if got, used := Rewrite("Fujiama", terms); got != want || len(used) != matches {
				t.Fatalf("rewrite = %q, %+v, want %q, %d terms", got, used, want, matches)
			}
		})
	}
}

func TestRewriteTermsInOrder(t *testing.T) {
	terms := []Term{{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, {Source: "lilToon", Reading: "りるとぅーん", Target: "lilToon"}, {Source: "薄荷", Reading: "はっか", Target: "Hakka"}}
	_, got := Rewrite("LILTOONとききょう、ききょう", terms)
	if !reflect.DeepEqual(got, terms[:2]) {
		t.Fatalf("matches = %+v, want %+v", got, terms[:2])
	}
	if _, got := Rewrite("unrelated", terms); len(got) != 0 {
		t.Fatal(got)
	}
	_, got = Rewrite("マヤ", []Term{{Source: "舞夜", Reading: "まや"}, {Source: "舞夜"}})
	if !reflect.DeepEqual(got, []Term{{Source: "舞夜", Reading: "まや"}}) {
		t.Fatalf("matched own output: %+v", got)
	}
}

func TestFoldPreservesRunePositions(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		if got := Fold(string(r)); utf8.RuneCountInString(got) != 1 {
			t.Fatalf("fold U+%04X = %q, want one rune", r, got)
		}
	}
}
