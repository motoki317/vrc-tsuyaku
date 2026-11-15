package asr

import (
	"reflect"
	"slices"
	"sync"
	"testing"
)

type hintDecoder struct {
	t                    *testing.T
	steps                [][]float32
	frames, accepted     []int
	duration, vocabulary int
}

func (d *hintDecoder) Scores(frame int) ([]float32, error) {
	d.t.Helper()
	i := len(d.frames)
	if i >= len(d.steps) {
		d.t.Fatal("unexpected decoder step")
	}
	d.frames = append(d.frames, frame)
	logits := append(slices.Clone(d.steps[i]), make([]float32, 5)...)
	logits[d.vocabulary+d.duration] = 10
	return logits, nil
}
func (d *hintDecoder) Accept(token int) error { d.accepted = append(d.accepted, token); return nil }

func (d *hintDecoder) checkpoint() (func() error, error) {
	frames, accepted := len(d.frames), len(d.accepted)
	return func() error {
		d.frames, d.accepted = d.frames[:frames-1], d.accepted[:accepted]
		return nil
	}, nil
}

func TestHintGreedyTDT(t *testing.T) {
	vocab := vocabulary{"x", "a", "bc", "d", "<blk>"}
	v := newHintVocabulary(vocab, false)
	for _, tc := range []struct {
		name  string
		words []string
		steps [][]float32
		want  []int
	}{
		{"near tie and blank preserves progress", []string{"abc"}, [][]float32{{0, -.5, -9, -9, -10}, {0, -9, -9, -9, 1}, {0, -9, -1, -9, -10}}, []int{1, 2}},
		{"blank cannot start a hint", []string{"abc"}, [][]float32{{-9, -.5, -9, -9, 0}}, nil},
		{"failed partial stops its bonus", []string{"abc"}, [][]float32{{0, -.5, -9, -9, -10}, {0, -9, -9, -9, -10}, {0, -9, -.5, -9, -10}}, []int{0, 0, 0}},
		{"overlapping hints take maximum", []string{"abc", "abcd", "abc"}, [][]float32{{0, -1.5, -9, -9, -10}}, []int{0}},
		{"empty words preserve tokens", nil, [][]float32{{0, -.5, -9, -9, -10}, {0, -9, -9, -9, 1}, {0, -9, -1, -9, -10}}, []int{0, 0}},
		{"unspellable suffix has no effect", []string{"abcz"}, [][]float32{{0, -.5, -9, -9, -10}}, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &hintDecoder{t: t, steps: tc.steps, duration: 1, vocabulary: len(vocab)}
			before := make([][]float32, len(tc.steps))
			for i := range tc.steps {
				before[i] = slices.Clone(tc.steps[i])
			}
			tokens, err := greedyTDT(t.Context(), len(tc.steps), tdtConfig{blank: 4, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile(tc.words)}, d)
			if err != nil || !slices.Equal(tokens, tc.want) || !slices.Equal(tokens, d.accepted) {
				t.Fatalf("tokens %v accepted %v error %v, want %v", tokens, d.accepted, err, tc.want)
			}
			if !reflect.DeepEqual(tc.steps, before) {
				t.Fatal("decoder changed scores")
			}
		})
	}
	// The exact same scripted decoder must emit the same tokens with nil and empty words.
	for _, words := range [][]string{nil, {}} {
		cfg := tdtConfig{blank: 2, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile(words)}
		d := &scriptedDecoder{t: t, scores: []scriptedScore{{0, 2, 0}, {1, 0, 0}, {1, 1, 2}, {3, 0, 1}}}
		got, err := greedyTDT(t.Context(), 4, cfg, d)
		if err != nil || !slices.Equal(got, []int{0, 1, 0}) {
			t.Fatalf("no words: %v %v", got, err)
		}
	}
	d := &hintDecoder{t: t, steps: [][]float32{{0, -.5, -9, -9, -10}, {0, -9, -1, -9, -10}}, duration: 2, vocabulary: len(vocab)}
	got, err := greedyTDT(t.Context(), 4, tdtConfig{blank: 4, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile([]string{"abc"})}, d)
	if err != nil || !slices.Equal(got, []int{1, 2}) || !slices.Equal(d.frames, []int{0, 2}) {
		t.Fatalf("duration changed: %v frames %v err %v", got, d.frames, err)
	}
}

func TestHintContinueOverBlank(t *testing.T) {
	h := newHintVocabulary(vocabulary{"a", "bc", "bd", "xyz", "<blk>"}, false).compile([]string{"abc", "abd", "abc", "xyz"})
	for _, tc := range []struct {
		name   string
		prefix []int
		logits []float32
		want   int
	}{
		{"no live hint", nil, []float32{-.5, -.5, -.5, -.5, 1}, 4},
		{"beats blank", []int{0}, []float32{-9, -.5, -9, -9, 1}, 1},
		{"ties blank", []int{0}, []float32{-9, -3, -9, -9, 1}, 4},
		{"loses to blank", []int{0}, []float32{-9, -3.25, -9, -9, 1}, 4},
		{"best continuation", []int{0}, []float32{-9, -.5, 0, -9, 1}, 2},
		{"unrelated start", []int{0}, []float32{-9, -9, -9, .5, 1}, 4},
		{"overlapping bonuses do not add", []int{0}, []float32{-9, -5, -9, -9, 1}, 4},
		{"completed hint", []int{0, 1}, []float32{-.5, -.5, -.5, -.5, 1}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := newHintState(h, 4)
			for i, token := range tc.prefix {
				state.accept(token, i)
			}
			if got := state.continueOverBlank(tc.logits, 4); got != tc.want {
				t.Fatalf("token %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHintMinimumLength(t *testing.T) {
	v := newHintVocabulary(vocabulary{"x", "し", "お", "り", "<blk>"}, false)
	steps := [][]float32{{0, 1, -9, -9, -10}, {0, -9, -.5, -9, -10}, {0, -9, -9, -.5, -10}}
	for _, tc := range []struct {
		word string
		want []int
	}{
		{"", []int{1, 0, 0}},
		{"し", []int{1, 0, 0}},
		{"しお", []int{1, 0, 0}},
		{"シオ", []int{1, 0, 0}},
		{"しおり", []int{1, 2, 3}},
	} {
		t.Run(tc.word, func(t *testing.T) {
			d := &hintDecoder{t: t, steps: steps, duration: 1, vocabulary: 5}
			got, err := greedyTDT(t.Context(), len(steps), tdtConfig{blank: 4, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile([]string{tc.word})}, d)
			if err != nil || !slices.Equal(got, tc.want) || !slices.Equal(got, d.accepted) {
				t.Fatalf("tokens %v state %v err %v, want %v", got, d.accepted, err, tc.want)
			}
		})
	}
}

func spellingBonuses(t *testing.T, h *compiledHints, tokens []int) []float32 {
	t.Helper()
	if h == nil {
		t.Fatal("no compiled hints")
	}
	edges := h.starts
	var bonuses []float32
	for _, token := range tokens {
		found := false
		for _, edge := range edges {
			if edge.token == token {
				bonuses = append(bonuses, edge.bonus)
				edges = h.nodes[edge.next].edges
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("token %d cannot continue spelling", token)
		}
	}
	return bonuses
}

func TestHintSpellings(t *testing.T) {
	v := newHintVocabulary(vocabulary{"▁Manuka", "▁Ma", "nu", "ka", "Ma", "▁ka", "▁", "<0xE7>", "<unk>", "<blk>"}, true)
	h := v.compile([]string{"mANUKA"})
	for _, tc := range []struct {
		tokens []int
		want   []float32
	}{
		{[]int{0}, []float32{1}},
		{[]int{1, 2, 3}, []float32{1, 8, 8}},
		{[]int{6, 4, 2, 3}, []float32{0, 1, 8, 8}},
	} {
		if got := spellingBonuses(t, h, tc.tokens); !slices.Equal(got, tc.want) {
			t.Fatalf("tokens %v: bonuses %v want %v", tc.tokens, got, tc.want)
		}
	}
	for _, edge := range h.starts {
		if edge.token == 4 {
			t.Fatal("unmarked piece starts a hint")
		}
	}
	if got := spellingBonuses(t, v.compile([]string{"Manu ka"}), []int{1, 2, 5}); !slices.Equal(got, []float32{1, 8, 8}) {
		t.Fatalf("space bonus %v", got)
	}
	if h := v.compile([]string{"狐狐狐", "Manukaz", "<0xE7>"}); h != nil {
		t.Fatal("unspellable or byte hints have an effect")
	}
	haas := newHintVocabulary(vocabulary{"▁ま", "ヌ", "か", "<blk>"}, false)
	if got := spellingBonuses(t, haas.compile([]string{"マヌカ"}), []int{0, 1, 2}); !slices.Equal(got, []float32{1, 8, 8}) {
		t.Fatalf("kana fold bonus %v", got)
	}
}

func TestHintConcurrentStates(t *testing.T) {
	h := newHintVocabulary(vocabulary{"x", "a", "bc", "<blk>"}, false).compile([]string{"abc"})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			state := newHintState(h, 3)
			if i%2 == 0 {
				state.accept(1, 0)
			} else {
				state.accept(0, 0)
			}
			got := state.choose([]float32{0, -9, -1}, 0)
			want := 0
			if i%2 == 0 {
				want = 2
			}
			if got != want {
				t.Errorf("state leaked: got %d want %d", got, want)
			}
		})
	}
	wg.Wait()
}

func TestHintConcurrentCompilation(t *testing.T) {
	v := newHintVocabulary(vocabulary{"x", "a", "bc", "d", "ef", "<blk>"}, false)
	steps := [][]float32{{0, -.5, -9, -.5, -9, -10}, {0, -9, -.5, -9, -.5, -10}}
	cases := []struct {
		words []string
		want  []int
	}{
		{nil, []int{0, 0}},
		{[]string{"abc"}, []int{1, 2}},
		{[]string{"def"}, []int{3, 4}},
		{[]string{"def", "abc"}, []int{1, 2}},
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 40 {
		wg.Go(func() {
			<-start
			tc := cases[i%len(cases)]
			cfg := tdtConfig{blank: 5, durations: []int{0, 1, 2, 3, 4}, maxSymbols: 10, hints: v.compile(tc.words)}
			d := &hintDecoder{t: t, steps: steps, duration: 1, vocabulary: 6}
			got, err := greedyTDT(t.Context(), len(steps), cfg, d)
			if err != nil || !slices.Equal(got, tc.want) || !slices.Equal(d.accepted, tc.want) {
				t.Errorf("words %v: tokens %v state %v err %v, want %v", tc.words, got, d.accepted, err, tc.want)
			}
		})
	}
	close(start)
	wg.Wait()
}
