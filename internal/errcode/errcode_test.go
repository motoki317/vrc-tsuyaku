package errcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodePreservesCauseAndDetail(t *testing.T) {
	cause := errors.New("diagnostic detail")
	err := fmt.Errorf("operation: %w", With(MicrophoneUnavailable, cause))
	if Of(err) != MicrophoneUnavailable || !errors.Is(err, cause) || err.Error() != "operation: diagnostic detail" {
		t.Fatalf("wrapped error: %q (%s)", err, Of(err))
	}
	if Of(cause) != Unknown || Of(nil) != "" || With(Template, nil) != nil {
		t.Fatal("unknown or successful result misclassified")
	}
}

func TestLocationPreservesCauseAndDetail(t *testing.T) {
	cause := errors.New("invalid word")
	err := fmt.Errorf("settings: %w", errors.Join(errors.New("invalid settings"), At(GlossaryRequired, 0, "Source", cause)))
	location := LocationOf(err)
	if Of(err) != GlossaryRequired || !errors.Is(err, cause) || location == nil || location.Index != 0 || location.Field != "Source" {
		t.Fatalf("located error: %v (%s, %+v)", err, Of(err), location)
	}
	if LocationOf(nil) != nil || LocationOf(cause) != nil || LocationOf(With(Template, cause)) != nil || At(GlossaryRequired, 0, "Source", nil) != nil {
		t.Fatal("unlocated or successful result has a location")
	}
}
