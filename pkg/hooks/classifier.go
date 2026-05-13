package hooks

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|password|authorization)\s*[:=]\s*)["']?[^"'\s,;}]+`)
	bearerTokenPattern      = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/\-]+=*`)
	commonAPIKeyPattern     = regexp.MustCompile(`(?i)\b(?:sk|ghp|github_pat)_[a-z0-9_]{16,}\b|\bsk-[a-z0-9_-]{20,}\b`)
)

func ClassifyEvent(event HookEvent) Decision {
	combined := redactedCombinedText(event)
	lowerCombined := strings.ToLower(combined)
	eventType := strings.ToLower(strings.TrimSpace(event.Event.Type))

	warnings := redactionWarnings(event, combined)

	if isExplicitMemoryRequest(eventType, lowerCombined) {
		return decisionWithWarnings(Decision{
			Action:     "candidate",
			ShouldSave: true,
			Signal:     "high",
			Reason:     "explicit user memory request",
			ProposedNote: &ProposedNote{
				Category:          "user-requirement",
				Title:             boundedTitle("Explicit memory request", combined),
				ActionableSummary: "[user-memory] " + boundedSummary(combined),
				Tags:              compactTags("hook-candidate", "explicit-memory", event.Agent.Name),
			},
		}, warnings)
	}

	if factKind, ok := verifiedOperationalFact(lowerCombined); ok {
		return decisionWithWarnings(Decision{
			Action:     "candidate",
			ShouldSave: true,
			Signal:     "high",
			Reason:     fmt.Sprintf("verified %s fact", factKind),
			ProposedNote: &ProposedNote{
				Category:          "project",
				Title:             boundedTitle(fmt.Sprintf("Verified %s fact", factKind), combined),
				ActionableSummary: fmt.Sprintf("[%s-fact] %s", factKind, boundedSummary(combined)),
				Tags:              compactTags("hook-candidate", factKind, event.Agent.Name, event.Session.Project),
			},
		}, warnings)
	}

	if isConcreteRootCause(lowerCombined) {
		return decisionWithWarnings(Decision{
			Action:       "candidate",
			ShouldSave:   false,
			Signal:       "medium",
			Reason:       "potential root-cause memory requires review",
			ProposedNote: nil,
		}, warnings)
	}

	if isConfigGotcha(lowerCombined) {
		return decisionWithWarnings(Decision{
			Action:       "candidate",
			ShouldSave:   false,
			Signal:       "medium",
			Reason:       "potential config gotcha requires review",
			ProposedNote: nil,
		}, warnings)
	}

	return decisionWithWarnings(NewSkipDecision("transient or low-signal hook event"), warnings)
}

func isExplicitMemoryRequest(eventType, lowerText string) bool {
	if eventType != "user_prompt" && eventType != "user_prompt_submit" {
		return false
	}
	if containsAny(lowerText, "do not remember", "don't remember", "不要记", "别记") {
		return false
	}
	return containsAny(lowerText,
		"remember this",
		"please remember",
		"save this",
		"note this",
		"add this to memory",
		"记住",
		"记一下",
		"请记住",
		"以后记得",
	)
}

func verifiedOperationalFact(lowerText string) (string, bool) {
	if containsAny(lowerText, "released successfully", "release published", "published successfully", "gh release create", "tagged successfully") {
		return "release", true
	}
	if containsAny(lowerText, "merged successfully", "pull request merged", "pr merged", "merge commit") {
		return "merge", true
	}
	if containsAny(lowerText, "deployed successfully", "deployment succeeded", "deployment completed") {
		return "deploy", true
	}
	return "", false
}

func isConcreteRootCause(lowerText string) bool {
	if containsAny(lowerText, "root cause: tbd", "root cause tbd", "root cause unknown") {
		return false
	}
	if !strings.Contains(lowerText, "root cause") {
		return false
	}
	hasFix := containsAny(lowerText, "fixed by", "fix:", "solution:", "resolved by", "repair:")
	hasEvidence := containsAny(lowerText, "verified", "evidence", ".go:", ".ts:", ".tsx:", ".py:", ".md:")
	return hasFix && hasEvidence
}

func isConfigGotcha(lowerText string) bool {
	return containsAny(lowerText, "config gotcha", "env gotcha", "configuration gotcha", "settings gotcha") &&
		containsAny(lowerText, "fix", "use ", "set ", "must ", "requires ")
}

func redactedCombinedText(event HookEvent) string {
	parts := []string{
		event.Text.UserPrompt,
		event.Text.AssistantSummary,
		event.Tool.InputSummary,
		event.Tool.OutputSummary,
	}
	return RedactSensitiveText(strings.Join(parts, "\n"))
}

func redactionWarnings(event HookEvent, redacted string) []string {
	original := strings.Join([]string{
		event.Text.UserPrompt,
		event.Text.AssistantSummary,
		event.Tool.InputSummary,
		event.Tool.OutputSummary,
	}, "\n")
	if original != redacted {
		return []string{"redacted_sensitive_text"}
	}
	return nil
}

func RedactSensitiveText(text string) string {
	text = secretAssignmentPattern.ReplaceAllString(text, "${1}[REDACTED]")
	text = bearerTokenPattern.ReplaceAllString(text, "Bearer [REDACTED]")
	text = commonAPIKeyPattern.ReplaceAllString(text, "[REDACTED_API_KEY]")
	return text
}

func boundedTitle(fallback, text string) string {
	line := firstInformativeLine(text)
	if line == "" {
		return fallback
	}
	return truncateRunes(line, 90)
}

func boundedSummary(text string) string {
	line := firstInformativeLine(text)
	if line == "" {
		return "review the normalized hook candidate before saving."
	}
	return truncateRunes(line, 180)
}

func firstInformativeLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line
	}
	return ""
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func compactTags(tags ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		tag = strings.ReplaceAll(tag, " ", "-")
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func decisionWithWarnings(decision Decision, warnings []string) Decision {
	if len(warnings) > 0 {
		decision.Warnings = warnings
	}
	return decision
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}
