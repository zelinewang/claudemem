package cmd

import (
	"fmt"
	"testing"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// TestHandleBackendFailure_NonTTY_DefaultIsFailLoud pins the "no silent
// fallback" contract for automation callers. In a test process stdin is
// non-TTY by default, so isInteractive() returns false. Without the
// explicit --auto-fallback-fts flag, we MUST return recoveryFail so the
// caller exits non-zero — guarantees cron/CI won't silently get worse
// results without the operator knowing.
func TestHandleBackendFailure_NonTTY_DefaultIsFailLoud(t *testing.T) {
	origInteractive := isInteractive
	t.Cleanup(func() {
		isInteractive = origInteractive
		searchAutoFallbackFTS = false
	})
	isInteractive = func() bool { return false } // force non-TTY regardless of invoker
	searchAutoFallbackFTS = false

	ebu := &vectors.ErrBackendUnavailable{
		Backend: "gemini",
		Cause:   fmt.Errorf("connection refused"),
		Hint:    "check $GEMINI_API_KEY or run `claudemem setup`",
	}

	choice, err := handleSpecificBackendFailure(ebu, true)
	if choice != recoveryFail {
		t.Errorf("expected recoveryFail (non-TTY default), got %v", choice)
	}
	if err == nil {
		t.Errorf("expected non-nil error to trigger exit 1, got nil")
	}
}

// TestHandleBackendFailure_NonTTY_AutoFallbackDegrades verifies that the
// explicit opt-in (--auto-fallback-fts) gives cron/hooks the graceful
// degradation they need while still writing a warning to stderr. The
// recoveryFTSOnly return tells the caller "use FTS this time" rather
// than exiting 1.
func TestHandleBackendFailure_NonTTY_AutoFallbackDegrades(t *testing.T) {
	origInteractive := isInteractive
	t.Cleanup(func() {
		isInteractive = origInteractive
		searchAutoFallbackFTS = false
	})
	isInteractive = func() bool { return false }
	searchAutoFallbackFTS = true

	ebu := &vectors.ErrBackendUnavailable{
		Backend: "gemini",
		Cause:   fmt.Errorf("503 Service Unavailable"),
		Hint:    "Gemini service brief outage; retry later",
	}

	choice, err := handleSpecificBackendFailure(ebu, true)
	if choice != recoveryFTSOnly {
		t.Errorf("expected recoveryFTSOnly with --auto-fallback-fts, got %v", choice)
	}
	if err != nil {
		t.Errorf("expected nil error (graceful degrade), got %v", err)
	}
}
