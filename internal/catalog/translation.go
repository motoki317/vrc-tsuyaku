package catalog

import (
	"fmt"
	"slices"
)

// TranslationAlias is the model name shared by llama-server and translation requests.
const TranslationAlias = "plamo-3-translate"

// Japanese and English are the default spoken languages.
const (
	Japanese = "ja"
	English  = "en"
)

var targetCodes = []string{
	"ar", "az", "be", "bg", "bn", "ca", "ceb", "de", "en", "es", "et", "eu",
	"fa", "fr", "gu", "he", "hi", "hr", "hu", "hy", "id", "it", "ja", "ja-easy",
	"jv", "ka", "kk", "km", "ko", "lt", "lv", "ml", "mr", "ms", "my", "ne", "nl",
	"pa", "pl", "ru", "si", "sk", "sl", "sr", "sw", "ta", "te", "th", "tl", "ur",
	"uz", "vi", "zh-hans", "zh-hant",
}

// SpokenLanguage maps a code to its recognizer. Auto marks a detection choice,
// which only the world flow can use.
type SpokenLanguage struct {
	Code       string `json:"code"`
	Recognizer string `json:"recognizer"`
	Auto       bool   `json:"auto"`
}

var spokenLanguages = []SpokenLanguage{
	{Code: Japanese, Recognizer: Haas},
	{Code: English, Recognizer: Parakeet},
}

var commonArtifacts = []string{ONNXRuntime, Silero, LlamaCPP, PLaMo}

// LanguageCatalog supplies the downloads for a language choice before it is saved.
type LanguageCatalog struct {
	Targets         []string         `json:"targets"`
	Spoken          []SpokenLanguage `json:"spoken"`
	CommonArtifacts []string         `json:"commonArtifacts"`
}

func Languages() LanguageCatalog {
	return LanguageCatalog{slices.Clone(targetCodes), slices.Clone(spokenLanguages), slices.Clone(commonArtifacts)}
}

func IsTarget(code string) bool {
	return slices.Contains(targetCodes, code)
}

func Spoken(code string) (SpokenLanguage, bool) {
	for _, language := range spokenLanguages {
		if language.Code == code {
			return language, true
		}
	}
	return SpokenLanguage{}, false
}

func Required(spoken string) ([]string, error) {
	language, ok := Spoken(spoken)
	if !ok {
		return nil, fmt.Errorf("unsupported spoken language %q", spoken)
	}
	return append(slices.Clone(commonArtifacts), language.Recognizer), nil
}

func recognizerLanguages(id string) []string {
	var codes []string
	for _, language := range spokenLanguages {
		if language.Recognizer == id && !language.Auto {
			codes = append(codes, language.Code)
		}
	}
	return codes
}
