package cmd

import (
	"strings"
	"testing"
)

func TestRunHookEventPayloadExplicitMemory(t *testing.T) {
	payload := `{
		"schema_version": "claudemem.hook_event.v1",
		"agent": {"name": "codex", "version": "test", "surface": "cli"},
		"event": {"type": "user_prompt_submit"},
		"text": {"user_prompt": "Please remember this: hook capture must stay candidate-first."}
	}`

	got, err := runHookEventPayload([]byte(payload))
	if err != nil {
		t.Fatalf("runHookEventPayload: %v", err)
	}

	if !got.ShouldSave {
		t.Fatal("expected explicit memory request to be save-eligible")
	}
	if got.ProposedNote == nil || got.ProposedNote.Category != "user-requirement" {
		t.Fatalf("proposed note=%#v, want user-requirement", got.ProposedNote)
	}
}

func TestRunHookEventPayloadRejectsUnknownSchema(t *testing.T) {
	payload := `{
		"schema_version": "claudemem.hook_event.v0",
		"event": {"type": "post_tool_use"}
	}`

	_, err := runHookEventPayload([]byte(payload))
	if err == nil {
		t.Fatal("expected schema validation error")
	}
	if !strings.Contains(err.Error(), "unsupported hook schema_version") {
		t.Fatalf("error=%q, want unsupported hook schema_version", err)
	}
}

func TestReadHookPayloadFromStdin(t *testing.T) {
	got, err := readHookPayload("-", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatalf("readHookPayload: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("payload=%q", string(got))
	}
}
