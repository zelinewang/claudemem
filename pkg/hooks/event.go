package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

const SchemaVersion = "claudemem.hook_event.v1"

type HookEvent struct {
	SchemaVersion string          `json:"schema_version"`
	Agent         AgentInfo       `json:"agent"`
	Event         EventInfo       `json:"event"`
	Session       SessionInfo     `json:"session,omitempty"`
	Tool          ToolInfo        `json:"tool,omitempty"`
	Text          TextInfo        `json:"text,omitempty"`
	Limits        Limits          `json:"limits,omitempty"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type AgentInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Surface string `json:"surface,omitempty"`
}

type EventInfo struct {
	Type       string `json:"type"`
	NativeType string `json:"native_type,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
}

type SessionInfo struct {
	ID      string `json:"id,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	Project string `json:"project,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

type ToolInfo struct {
	Name          string `json:"name,omitempty"`
	InputSummary  string `json:"input_summary,omitempty"`
	OutputSummary string `json:"output_summary,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
}

type TextInfo struct {
	UserPrompt       string `json:"user_prompt,omitempty"`
	AssistantSummary string `json:"assistant_summary,omitempty"`
}

type Limits struct {
	MaxPayloadBytes int  `json:"max_payload_bytes,omitempty"`
	AllowNetwork    bool `json:"allow_network,omitempty"`
}

type Decision struct {
	Action       string        `json:"action"`
	ShouldSave   bool          `json:"should_save"`
	Signal       string        `json:"signal"`
	Reason       string        `json:"reason"`
	ProposedNote *ProposedNote `json:"proposed_note"`
	Warnings     []string      `json:"warnings,omitempty"`
}

type ProposedNote struct {
	Category          string   `json:"category"`
	Title             string   `json:"title"`
	ActionableSummary string   `json:"actionable_summary"`
	Tags              []string `json:"tags"`
}

func EvaluateEventJSON(data []byte) (Decision, error) {
	event, err := DecodeEventJSON(data)
	if err != nil {
		return Decision{}, err
	}
	return ClassifyEvent(event), nil
}

func DecodeEventJSON(data []byte) (HookEvent, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return HookEvent{}, fmt.Errorf("empty hook payload")
	}

	var event HookEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return HookEvent{}, fmt.Errorf("decode hook payload: %w", err)
	}

	if strings.TrimSpace(event.SchemaVersion) != SchemaVersion {
		return HookEvent{}, fmt.Errorf("unsupported hook schema_version %q", event.SchemaVersion)
	}
	if strings.TrimSpace(event.Event.Type) == "" {
		return HookEvent{}, fmt.Errorf("hook event.type is required")
	}

	return event, nil
}

func NewSkipDecision(reason string) Decision {
	return Decision{
		Action:       "candidate",
		ShouldSave:   false,
		Signal:       "low",
		Reason:       reason,
		ProposedNote: nil,
	}
}
