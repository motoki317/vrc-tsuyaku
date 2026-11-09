package catalog

import (
	"fmt"
	"slices"
)

// TranslationAlias is the model name shared by llama-server and translation requests.
const TranslationAlias = "plamo-3-translate"

const (
	Translator2B  = "plamo-3-translate-2b"
	Translator08B = "plamo-3-translate-0.8b"
)

type Translator struct {
	ID       string  `json:"id"`
	Artifact string  `json:"artifact"`
	Default  bool    `json:"default"`
	Speed    float64 `json:"speed"`
}

var translators = []Translator{
	{ID: Translator2B, Artifact: PLaMo, Default: true, Speed: 1},
	{ID: Translator08B, Artifact: PLaMo08B, Speed: 562588500.0 / 158268955},
}

func Translators() []Translator { return slices.Clone(translators) }

func LookupTranslator(id string) (Translator, bool) {
	for _, translator := range translators {
		if translator.ID == id {
			return translator, true
		}
	}
	return Translator{}, false
}

func DefaultTranslator() string {
	for _, translator := range translators {
		if translator.Default {
			return translator.ID
		}
	}
	panic("catalog has no default translator")
}

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
	{Code: "bg", Recognizer: ParakeetV3},
	{Code: "de", Recognizer: ParakeetV3},
	{Code: "es", Recognizer: ParakeetV3},
	{Code: "et", Recognizer: ParakeetV3},
	{Code: "fr", Recognizer: ParakeetV3},
	{Code: "hr", Recognizer: ParakeetV3},
	{Code: "hu", Recognizer: ParakeetV3},
	{Code: "it", Recognizer: ParakeetV3},
	{Code: "lt", Recognizer: ParakeetV3},
	{Code: "lv", Recognizer: ParakeetV3},
	{Code: "nl", Recognizer: ParakeetV3},
	{Code: "pl", Recognizer: ParakeetV3},
	{Code: "pt", Recognizer: ParakeetV3},
	{Code: "ru", Recognizer: ParakeetV3},
	{Code: "sk", Recognizer: ParakeetV3},
	{Code: "sl", Recognizer: ParakeetV3},
	{Code: "sv", Recognizer: ParakeetV3},
	{Code: "auto", Recognizer: ParakeetV3, Auto: true},
}

var commonArtifacts = []string{ONNXRuntime, Silero, LlamaCPP}

// LanguageCatalog supplies the downloads for a language choice before it is saved.
type LanguageCatalog struct {
	Targets         []string         `json:"targets"`
	Spoken          []SpokenLanguage `json:"spoken"`
	CommonArtifacts []string         `json:"commonArtifacts"`
	Translators     []Translator     `json:"translators"`
}

func Languages() LanguageCatalog {
	return LanguageCatalog{slices.Clone(targetCodes), slices.Clone(spokenLanguages), slices.Clone(commonArtifacts), Translators()}
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

// TranslatorSource returns the PLaMo source for a spoken choice. Auto and
// supported spoken languages outside PLaMo's list omit the source. Other codes
// pass through unchanged.
func TranslatorSource(code string) string {
	if language, ok := Spoken(code); ok && (language.Auto || !IsTarget(code)) {
		return ""
	}
	return code
}

func Required(spoken, translator string) ([]string, error) {
	language, ok := Spoken(spoken)
	if !ok {
		return nil, fmt.Errorf("unsupported spoken language %q", spoken)
	}
	choice, ok := LookupTranslator(translator)
	if !ok {
		return nil, fmt.Errorf("unsupported translator %q", translator)
	}
	return append(slices.Clone(commonArtifacts), choice.Artifact, language.Recognizer), nil
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
