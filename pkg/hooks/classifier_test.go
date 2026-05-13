package hooks

import "testing"

func TestClassifyExplicitMemoryRequest(t *testing.T) {
	event := baseTestEvent("user_prompt_submit")
	event.Text.UserPrompt = "Please remember this: Codex and Claude Code are P0 adapters for claudemem."

	got := ClassifyEvent(event)

	if !got.ShouldSave {
		t.Fatal("explicit memory request should be save-eligible")
	}
	if got.Signal != "high" {
		t.Fatalf("signal=%q, want high", got.Signal)
	}
	if got.ProposedNote == nil || got.ProposedNote.Category != "user-requirement" {
		t.Fatalf("proposed note=%#v, want user-requirement", got.ProposedNote)
	}
}

func TestClassifyTransientToolOutput(t *testing.T) {
	event := baseTestEvent("post_tool_use")
	event.Tool.Name = "Bash"
	event.Tool.OutputSummary = "ok github.com/zelinewang/claudemem/pkg/hooks 0.012s\nPASS"

	got := ClassifyEvent(event)

	if got.ShouldSave {
		t.Fatal("plain test output should not be save-eligible")
	}
	if got.Signal != "low" {
		t.Fatalf("signal=%q, want low", got.Signal)
	}
}

func TestClassifyVerifiedReleaseFact(t *testing.T) {
	event := baseTestEvent("post_tool_use")
	event.Tool.Name = "gh"
	event.Tool.OutputSummary = "Release v3.0.11 published successfully and PR checks passed."
	event.Session.Project = "claudemem"

	got := ClassifyEvent(event)

	if !got.ShouldSave {
		t.Fatal("verified release fact should be save-eligible")
	}
	if got.ProposedNote == nil || got.ProposedNote.Category != "project" {
		t.Fatalf("proposed note=%#v, want project category", got.ProposedNote)
	}
	if !containsTag(got.ProposedNote.Tags, "release") {
		t.Fatalf("tags=%#v, want release", got.ProposedNote.Tags)
	}
}

func TestClassifyReleaseTestOutputIsNotReleaseFact(t *testing.T) {
	event := baseTestEvent("post_tool_use")
	event.Tool.Name = "Bash"
	event.Tool.OutputSummary = "Release tests for v3.0.11 passed."

	got := ClassifyEvent(event)

	if got.ShouldSave {
		t.Fatal("release test output should not be treated as a verified release fact")
	}
	if got.Signal != "low" {
		t.Fatalf("signal=%q, want low", got.Signal)
	}
}

func TestClassifyDoNotRememberIsNotMemoryRequest(t *testing.T) {
	event := baseTestEvent("user_prompt_submit")
	event.Text.UserPrompt = "Do not remember this temporary workaround."

	got := ClassifyEvent(event)

	if got.ShouldSave {
		t.Fatal("negative memory request should not be save-eligible")
	}
}

func TestClassifyRootCauseTemplateNoise(t *testing.T) {
	event := baseTestEvent("post_tool_use")
	event.Text.AssistantSummary = "Root Cause: TBD\nFix: TBD\nEvidence: TBD"

	got := ClassifyEvent(event)

	if got.ShouldSave {
		t.Fatal("template root cause text should not be save-eligible")
	}
	if got.Signal != "low" {
		t.Fatalf("signal=%q, want low", got.Signal)
	}
}

func TestClassifyConcreteRootCauseAsReviewCandidate(t *testing.T) {
	event := baseTestEvent("post_tool_use")
	event.Text.AssistantSummary = "Root cause: cmd/sync.go:88 returned text even when --format json. Fixed by routing status through OutputJSON. Verified with go test ./cmd."

	got := ClassifyEvent(event)

	if got.ShouldSave {
		t.Fatal("root cause candidates should require review in the first deterministic pass")
	}
	if got.Signal != "medium" {
		t.Fatalf("signal=%q, want medium", got.Signal)
	}
}

func TestRedactSensitiveText(t *testing.T) {
	event := baseTestEvent("user_prompt_submit")
	event.Text.UserPrompt = "Please remember token=secret-value and Authorization: Bearer abc123def456"

	got := ClassifyEvent(event)

	if got.ProposedNote == nil {
		t.Fatal("expected proposed note")
	}
	if got.ProposedNote.Title == event.Text.UserPrompt {
		t.Fatal("expected redacted title")
	}
	if len(got.Warnings) == 0 || got.Warnings[0] != "redacted_sensitive_text" {
		t.Fatalf("warnings=%#v, want redaction warning", got.Warnings)
	}
}

func baseTestEvent(eventType string) HookEvent {
	return HookEvent{
		SchemaVersion: SchemaVersion,
		Agent: AgentInfo{
			Name:    "codex",
			Version: "test",
			Surface: "cli",
		},
		Event: EventInfo{Type: eventType},
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
