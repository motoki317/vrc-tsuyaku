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
		{"ja", "en", []Term{{"桔梗", "ききょう", "Kikyo"}, {"マヌカ", "まぬか", "Manuka"}}},
		{"en", "ja", []Term{{"Kikyo", "", "桔梗"}, {"Manuka", "", "マヌカ"}}},
		{"en", "ja-easy", []Term{{"Kikyo", "", "桔梗"}, {"Manuka", "", "マヌカ"}}},
		{"fr", "ja", []Term{{"Renard", "", "狐"}}},
		{"en", "fr", []Term{{"Fox", "", "Renard"}}},
		{"ja", "de", nil},
		{"auto", "ja", []Term{{"Kikyo", "", "桔梗"}, {"Manuka", "", "マヌカ"}, {"Renard", "", "狐"}}},
		{"pt", "ja-easy", []Term{{"Kikyo", "", "桔梗"}, {"Manuka", "", "マヌカ"}, {"Renard", "", "狐"}}},
		{"sv", "en", []Term{{"桔梗", "", "Kikyo"}, {"マヌカ", "", "Manuka"}, {"Renard", "", "Fox"}}},
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
	want := []Term{{"lilToon", "りるとぅーん", "lilToon"}, {"べつ", "", "Different"}, {"マヌカ", "まぬか", "Manuka"}}
	if got := Terms(entries, "ja", "en"); !reflect.DeepEqual(got, want) {
		t.Fatalf("terms = %+v, want %+v", got, want)
	}
}
