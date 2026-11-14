// Package errcode identifies the player's next action without matching diagnostic text.
package errcode

import "errors"

type Code string

const (
	// Unknown tells the page to show the server's diagnostic text.
	Unknown Code = "unknown"
	// SettingsChanged asks the page to reload settings and reapply the player's change.
	SettingsChanged Code = "settings_changed"
	// MissingDownloads asks the player to download the flow's missing artifacts.
	MissingDownloads Code = "missing_downloads"
	// MicrophoneUnavailable asks the player to select an available microphone in Audio settings and retry.
	MicrophoneUnavailable Code = "microphone_unavailable"
	// ApplicationNotRunning asks the player to start VRChat and its playback, then retry the world flow.
	ApplicationNotRunning Code = "application_not_running"
	// ApplicationEnded asks the player to resume VRChat playback, then retry the world flow.
	ApplicationEnded Code = "application_ended"
	// MultipleApplications asks the player to close duplicate VRChat instances or playback streams, then retry.
	MultipleApplications Code = "multiple_applications"
	// ChatboxSend asks the player to check the address in Chatbox settings, then retry the self flow.
	ChatboxSend Code = "chatbox_send"
	// Template asks the player to fix the template in Chatbox settings.
	Template Code = "template"
	// TranslatorExited asks the player to retry the flow to restart the translator.
	TranslatorExited Code = "translator_exited"
	// DownloadHTTP asks the player to check the connection, then retry the download.
	DownloadHTTP Code = "download_http"
	// DownloadIntegrity asks the player to retry the download to replace damaged files.
	DownloadIntegrity Code = "download_integrity"
	// DiskWrite asks the player to choose a data folder with free space and write permission, then retry.
	DiskWrite Code = "disk_write"
	// GlossaryLimit asks the player to remove glossary entries before adding more.
	GlossaryLimit Code = "glossary_limit"
	// GlossaryLanguage asks the player to choose a supported translation language.
	GlossaryLanguage Code = "glossary_language"
	// GlossarySameLanguage asks the player to choose different source and target languages.
	GlossarySameLanguage Code = "glossary_same_language"
	// GlossaryRequired asks the player to enter a word.
	GlossaryRequired Code = "glossary_required"
	// GlossaryControl asks the player to remove control characters and line breaks.
	GlossaryControl Code = "glossary_control"
	// GlossaryLength asks the player to shorten the word or reading.
	GlossaryLength Code = "glossary_length"
	// GlossaryReading asks the player to use kana for a Japanese word's reading.
	GlossaryReading Code = "glossary_reading"
)

// Location uses a zero-based index in the validated list and the field's settings JSON name.
type Location struct {
	Index int    `json:"index"`
	Field string `json:"field"`
}

type coded struct {
	code     Code
	err      error
	location *Location
}

func (e *coded) Error() string { return e.err.Error() }
func (e *coded) Unwrap() error { return e.err }

func With(code Code, err error) error {
	if err == nil {
		return nil
	}
	return &coded{code: code, err: err}
}

func At(code Code, index int, field string, err error) error {
	if err == nil {
		return nil
	}
	return &coded{code: code, err: err, location: &Location{Index: index, Field: field}}
}

func LocationOf(err error) *Location {
	var e *coded
	if errors.As(err, &e) {
		return e.location
	}
	return nil
}

func Of(err error) Code {
	if err == nil {
		return ""
	}
	var e *coded
	if errors.As(err, &e) {
		return e.code
	}
	return Unknown
}
