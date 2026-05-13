package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type EvalCase struct {
	Name     string           `json:"name"`
	Event    HookEvent        `json:"event"`
	Expected ExpectedDecision `json:"expected"`
}

type ExpectedDecision struct {
	ShouldSave *bool  `json:"should_save,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Category   string `json:"category,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type EvalReport struct {
	Total     int              `json:"total"`
	Passed    int              `json:"passed"`
	Failed    int              `json:"failed"`
	TruePos   int              `json:"true_positive"`
	FalsePos  int              `json:"false_positive"`
	TrueNeg   int              `json:"true_negative"`
	FalseNeg  int              `json:"false_negative"`
	Precision float64          `json:"precision"`
	Recall    float64          `json:"recall"`
	F1        float64          `json:"f1"`
	Cases     []EvalCaseResult `json:"cases"`
}

type EvalCaseResult struct {
	Name     string           `json:"name"`
	Pass     bool             `json:"pass"`
	Expected ExpectedDecision `json:"expected"`
	Actual   Decision         `json:"actual"`
	Errors   []string         `json:"errors,omitempty"`
}

func RunEvalDir(dir string) (EvalReport, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return EvalReport{}, fmt.Errorf("read eval fixtures: %w", err)
	}

	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)

	if len(paths) == 0 {
		return EvalReport{}, fmt.Errorf("no eval fixtures found in %s", dir)
	}

	report := EvalReport{}
	for _, path := range paths {
		result, err := runEvalCase(path)
		if err != nil {
			return EvalReport{}, err
		}
		report.Cases = append(report.Cases, result)
		report.Total++
		if result.Pass {
			report.Passed++
		} else {
			report.Failed++
		}

		if result.Expected.ShouldSave != nil {
			expectedPositive := *result.Expected.ShouldSave
			actualPositive := result.Actual.ShouldSave
			switch {
			case expectedPositive && actualPositive:
				report.TruePos++
			case expectedPositive && !actualPositive:
				report.FalseNeg++
			case !expectedPositive && actualPositive:
				report.FalsePos++
			default:
				report.TrueNeg++
			}
		}
	}

	report.Precision = ratio(report.TruePos, report.TruePos+report.FalsePos)
	report.Recall = ratio(report.TruePos, report.TruePos+report.FalseNeg)
	if report.Precision+report.Recall > 0 {
		report.F1 = 2 * report.Precision * report.Recall / (report.Precision + report.Recall)
	}

	return report, nil
}

func runEvalCase(path string) (EvalCaseResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EvalCaseResult{}, fmt.Errorf("read eval fixture %s: %w", path, err)
	}

	var evalCase EvalCase
	if err := json.Unmarshal(data, &evalCase); err != nil {
		return EvalCaseResult{}, fmt.Errorf("decode eval fixture %s: %w", path, err)
	}
	if evalCase.Name == "" {
		evalCase.Name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	encodedEvent, err := json.Marshal(evalCase.Event)
	if err != nil {
		return EvalCaseResult{}, fmt.Errorf("encode eval event %s: %w", path, err)
	}
	if _, err := DecodeEventJSON(encodedEvent); err != nil {
		return EvalCaseResult{}, fmt.Errorf("validate eval fixture %s: %w", path, err)
	}

	actual := ClassifyEvent(evalCase.Event)
	result := EvalCaseResult{
		Name:     evalCase.Name,
		Expected: evalCase.Expected,
		Actual:   actual,
	}

	if evalCase.Expected.ShouldSave != nil && actual.ShouldSave != *evalCase.Expected.ShouldSave {
		result.Errors = append(result.Errors, fmt.Sprintf("should_save=%v want %v", actual.ShouldSave, *evalCase.Expected.ShouldSave))
	}
	if evalCase.Expected.Signal != "" && actual.Signal != evalCase.Expected.Signal {
		result.Errors = append(result.Errors, fmt.Sprintf("signal=%q want %q", actual.Signal, evalCase.Expected.Signal))
	}
	if evalCase.Expected.Category != "" {
		actualCategory := ""
		if actual.ProposedNote != nil {
			actualCategory = actual.ProposedNote.Category
		}
		if actualCategory != evalCase.Expected.Category {
			result.Errors = append(result.Errors, fmt.Sprintf("category=%q want %q", actualCategory, evalCase.Expected.Category))
		}
	}
	if evalCase.Expected.Reason != "" && actual.Reason != evalCase.Expected.Reason {
		result.Errors = append(result.Errors, fmt.Sprintf("reason=%q want %q", actual.Reason, evalCase.Expected.Reason))
	}

	result.Pass = len(result.Errors) == 0
	return result, nil
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
