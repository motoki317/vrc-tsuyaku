package pipeline

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/motoki317/vrc-tsuyaku/internal/glossary"
)

func TestGlossarySnapshotRewrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := fakeStages()
		first := []glossary.Term{{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, {Source: "lilToon", Reading: "りるとぅーん", Target: "lilToon"}, {Source: "薄荷", Reading: "はっか", Target: "Hakka"}}
		second := []glossary.Term{{Source: "菊響", Reading: "ききょう", Target: "Kikyou"}, first[1], first[2]}
		current := first
		settingsCalls, recognized := 0, 0
		s.Settings = func() PhraseSettings {
			settingsCalls++
			return PhraseSettings{SourceLang: "ja", TargetLang: "en", Template: "{{.Original}}", Terms: current}
		}
		s.Recognize = func(_ context.Context, _ []float32, settings PhraseSettings) (string, error) {
			recognized++
			want := second
			if recognized == 1 {
				want = first
			}
			if !reflect.DeepEqual(settings.Terms, want) {
				t.Errorf("recognizer terms = %+v, want %+v", settings.Terms, want)
			}
			current = second
			if recognized == 3 {
				return "control", nil
			}
			return "ききょう リルトゥーン2を入れた", nil
		}
		wantTexts := []string{"桔梗 lilToon2を入れた", "菊響 lilToon2を入れた", "control"}
		translated := 0
		s.Translate = func(_ context.Context, text string, settings PhraseSettings) (string, error) {
			wantTerms := [][]glossary.Term{first[:2], second[:2], nil}
			if text != wantTexts[translated] || !reflect.DeepEqual(settings.Terms, wantTerms[translated]) {
				t.Errorf("translation %d = %q, %+v", translated, text, settings.Terms)
			}
			translated++
			return "translated " + text, nil
		}
		var pages []string
		s.Send = func(_ context.Context, text string) error { pages = append(pages, text); return nil }
		var mu sync.Mutex
		var events []Event
		s.Emit = func(event Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}
		if err := Run(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		var recognizedTexts, translatedOriginals []string
		for _, event := range events {
			switch event.Kind {
			case "recognized":
				recognizedTexts = append(recognizedTexts, event.Text)
			case "translated":
				translatedOriginals = append(translatedOriginals, event.Original)
			}
		}
		if settingsCalls != 3 || translated != 3 || !reflect.DeepEqual(recognizedTexts, wantTexts) || !reflect.DeepEqual(translatedOriginals, wantTexts) {
			t.Fatalf("settings calls %d, translations %d, recognized %v, originals %v", settingsCalls, translated, recognizedTexts, translatedOriginals)
		}
		wantPages := []string{"桔梗 lilToon2を入れた", "桔梗 lilToon2を入れた 菊響 lilToon2を入れた", "桔梗 lilToon2を入れた 菊響 lilToon2を入れた control"}
		if !reflect.DeepEqual(pages, wantPages) {
			t.Fatalf("chatbox pages = %v, want %v", pages, wantPages)
		}
	})
}
