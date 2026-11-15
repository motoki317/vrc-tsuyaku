package glossary

import (
	"reflect"
	"testing"
)

func TestTerms(t *testing.T) {
	entries := []Entry{
		{SourceLang: "ja", Source: "桔梗", Reading: "ききょう", TargetLang: "en", Target: "Kikyo"},
		{SourceLang: "en", Source: "Manuka", TargetLang: "ja", Target: "マヌカ", Reading: "まぬか"},
		{SourceLang: "fr", Source: "Renard", TargetLang: "ja", Target: "狐", Reading: "きつね"},
		{SourceLang: "en", Source: "Fox", TargetLang: "fr", Target: "Renard"},
	}
	for _, tc := range []struct {
		source, target string
		want           []Term
	}{
		{"ja", "en", []Term{{Source: "桔梗", Reading: "ききょう", Target: "Kikyo"}, {Source: "マヌカ", Reading: "まぬか", Target: "Manuka"}}},
		{"en", "ja", []Term{{Source: "Kikyo", Target: "桔梗", soundKey: "kikio"}, {Source: "Manuka", Target: "マヌカ", soundKey: "manuka"}}},
		{"en", "ja-easy", []Term{{Source: "Kikyo", Target: "桔梗", soundKey: "kikio"}, {Source: "Manuka", Target: "マヌカ", soundKey: "manuka"}}},
		{"fr", "ja", []Term{{Source: "Renard", Target: "狐"}}},
		{"en", "fr", []Term{{Source: "Fox", Target: "Renard"}}},
		{"ja", "de", nil},
		{"auto", "ja", []Term{{Source: "Kikyo", Target: "桔梗"}, {Source: "Manuka", Target: "マヌカ"}, {Source: "Renard", Target: "狐"}}},
		{"pt", "ja-easy", []Term{{Source: "Kikyo", Target: "桔梗"}, {Source: "Manuka", Target: "マヌカ"}, {Source: "Renard", Target: "狐"}}},
		{"sv", "en", []Term{{Source: "桔梗", Target: "Kikyo"}, {Source: "マヌカ", Target: "Manuka"}, {Source: "Renard", Target: "Fox"}}},
	} {
		t.Run(tc.source+"/"+tc.target, func(t *testing.T) {
			if got := Terms(entries, tc.source, tc.target); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("terms = %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := Terms(nil, "ja", "en"); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestTermCollisions(t *testing.T) {
	entries := []Entry{
		{SourceLang: "ja", Source: "lilToon", Reading: "りるとぅーん", TargetLang: "en", Target: "lilToon"},
		{SourceLang: "en", Source: "Other", TargetLang: "ja", Target: "LILTOON", Reading: "べつ"},
		{SourceLang: "ja", Source: "リルトゥーン", TargetLang: "en", Target: "Other"},
		{SourceLang: "ja", Source: "別", Reading: "lilToon", TargetLang: "en", Target: "Other"},
		{SourceLang: "ja", Source: "別", Reading: "リルトゥーン", TargetLang: "en", Target: "Other"},
		{SourceLang: "ja", Source: "べつ", TargetLang: "en", Target: "Different"},
		{SourceLang: "ja", Source: "マヌカ", Reading: "まぬか", TargetLang: "en", Target: "Manuka"},
	}
	want := []Term{{Source: "lilToon", Reading: "りるとぅーん", Target: "lilToon"}, {Source: "べつ", Target: "Different"}, {Source: "マヌカ", Reading: "まぬか", Target: "Manuka"}}
	if got := Terms(entries, "ja", "en"); !reflect.DeepEqual(got, want) {
		t.Fatalf("terms = %+v, want %+v", got, want)
	}
}

func TestTermSoundKeys(t *testing.T) {
	for _, tc := range []struct{ word, source, want string }{
		{"FUJIYAMA", "en", "fujiama"},
		{"Kikyo", "en", "kikio"},
		{"Lime", "en", ""},
		{"Ziiii", "en", ""},
		{"Fuji Yama", "en", ""},
		{"Fuji-Yama", "en", ""},
		{"Fuji2ama", "en", ""},
		{"Fuji'ama", "en", ""},
		{"Fujiyamá", "en", ""},
		{"FUJIYAMA", "ja", ""},
		{"FUJIYAMA", "fr", ""},
		{"FUJIYAMA", "auto", ""},
		{"FUJIYAMA", "pt", ""},
	} {
		t.Run(tc.word+"/"+tc.source, func(t *testing.T) {
			entrySource := tc.source
			if entrySource == "auto" || entrySource == "pt" {
				entrySource = "en"
			}
			entries := []Entry{{SourceLang: entrySource, Source: tc.word, TargetLang: "de", Target: "target"}}
			got := Terms(entries, tc.source, "de")
			if len(got) != 1 || got[0].soundKey != tc.want {
				t.Fatalf("terms = %+v, want key %q", got, tc.want)
			}
		})
	}
}
