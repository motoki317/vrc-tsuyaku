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
		{"reading", "ききょうとキキョウ", "桔梗と桔梗", Term{"桔梗", "ききょう", "Kikyo"}, false},
		{"canonical", "桔梗と桔梗", "桔梗と桔梗", Term{"桔梗", "ききょう", "Kikyo"}, true},
		{"case", "LILTOONを入れた", "lilToonを入れた", Term{"lilToon", "", "lilToon"}, true},
		{"particle", "vrcfuryを入れた", "VRCFuryを入れた", Term{"VRCFury", "", "VRCFury"}, true},
		{"after long vowel", "ユーザーliltoon", "ユーザーlilToon", Term{"lilToon", "", "lilToon"}, true},
		{"inside word", "sublime", "sublime", Term{"Lime", "", "ライム"}, false},
		{"suffix", "Limes", "Limes", Term{"Lime", "", "ライム"}, false},
		{"digit prefix", "2Lime", "2Lime", Term{"Lime", "", "ライム"}, false},
		{"digit suffix", "Lime2", "Lime2", Term{"Lime", "", "ライム"}, false},
		{"punctuation", "(lime), lime!", "(Lime), Lime!", Term{"Lime", "", "ライム"}, true},
		{"different spaced scripts", "Жlimeα", "Жlimeα", Term{"Lime", "", "ライム"}, false},
		{"hangul", "한lime", "한lime", Term{"Lime", "", "ライム"}, false},
		{"nonspace scripts", "林limeไทย", "林Limeไทย", Term{"Lime", "", "ライム"}, true},
		{"kana fold", "まぬかって", "マヌカって", Term{"マヌカ", "", "Manuka"}, true},
		{"katakana reading suffix", "リンドの衣装", "リンドの衣装", Term{"凛", "りん", "Rin"}, false},
		{"katakana source suffix", "リンドの衣装", "リンドの衣装", Term{"リン", "", "Rin"}, false},
		{"katakana prefix", "プリンの衣装", "プリンの衣装", Term{"リン", "", "Rin"}, false},
		{"katakana long vowel prefix", "リーリン", "リーリン", Term{"リン", "", "Rin"}, false},
		{"katakana long vowel end", "リード", "リード", Term{"リー", "", "Rii"}, false},
		{"short hiragana reading inside word", "ちゃんとわかる", "ちゃんとわかる", Term{"永遠", "とわ", "Towa"}, false},
		{"short katakana source inside hiragana", "いまやってる", "いまやってる", Term{Source: "マヤ"}, false},
		{"short katakana source inside hiragana word", "ちゃんとわかるよ", "ちゃんとわかるよ", Term{Source: "トワ"}, false},
		{"short reading with long vowel", "まーね", "まーね", Term{"真亜", "まー", "Maa"}, false},
		{"short source with long vowel", "まーね", "まーね", Term{Source: "マー"}, false},
		{"short hiragana source with long vowel", "まーね", "まーね", Term{Source: "まー"}, true},
		{"short katakana reading", "トワ", "永遠", Term{"永遠", "とわ", "Towa"}, false},
		{"short mixed kana reading", "とワ", "永遠", Term{"永遠", "とわ", "Towa"}, false},
		{"short hiragana reading before particle", "まやの衣装", "まやの衣装", Term{"舞夜", "まや", "Maya"}, false},
		{"short katakana reading before particle", "マヤの衣装", "舞夜の衣装", Term{"舞夜", "まや", "Maya"}, false},
		{"single hiragana reading", "か", "か", Term{"華", "か", "Ka"}, false},
		{"single katakana reading", "カ", "華", Term{"華", "か", "Ka"}, false},
		{"three character hiragana reading", "すずやの衣装", "紗夜の衣装", Term{"紗夜", "すずや", "Suzuya"}, false},
		{"short hiragana source", "しおの衣装", "しおの衣装", Term{"しお", "しお", "Shio"}, true},
		{"short source folded", "シオの衣装", "しおの衣装", Term{"しお", "しお", "Shio"}, true},
		{"mixed kana reading", "スズや", "紗夜", Term{"紗夜", "すずや", "Suzuya"}, false},
		{"katakana particle", "リンの衣装", "凛の衣装", Term{"凛", "りん", "Rin"}, false},
		{"katakana small tsu", "マヌカッテ", "マヌカッテ", Term{"マヌカ", "", "Manuka"}, false},
		{"long vowel", "ハッカー", "ハッカー", Term{"薄荷", "はっか", "Hakka"}, false},
		{"canonical long vowel", "ハッカー", "ハッカー", Term{"ハッカ", "", "Hakka"}, false},
		{"small kana", "キキョウゃ", "キキョウゃ", Term{"桔梗", "ききょう", "Kikyo"}, false},
		{"long source", "リルトゥーンー", "リルトゥーンー", Term{"lilToon", "りるとぅーん", "lilToon"}, false},
		{"reading before digit", "リルトゥーン2を入れた", "lilToon2を入れた", Term{"lilToon", "りるとぅーん", "lilToon"}, false},
		{"small tsu", "ききょうっ", "桔梗っ", Term{"桔梗", "ききょう", "Kikyo"}, false},
		{"partial reading", "ききょう", "ききょう", Term{"菊", "きく", "Kiku"}, false},
		{"multibyte offsets", "😀K ききょう。", "😀K K。", Term{"K", "ききょう", "Kay"}, true},
		{"empty", "", "", Term{"Lime", "", "ライム"}, false},
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

func TestRewriteTermsInOrder(t *testing.T) {
	terms := []Term{{"桔梗", "ききょう", "Kikyo"}, {"lilToon", "りるとぅーん", "lilToon"}, {"薄荷", "はっか", "Hakka"}}
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
