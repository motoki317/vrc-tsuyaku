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
