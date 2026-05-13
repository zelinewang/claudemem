package hooks

import "testing"

func TestRunEvalDir(t *testing.T) {
	report, err := RunEvalDir("testdata/eval")
	if err != nil {
		t.Fatalf("RunEvalDir: %v", err)
	}

	if report.Total < 4 {
		t.Fatalf("total=%d, want at least 4", report.Total)
	}
	if report.Failed != 0 {
		t.Fatalf("failed=%d, cases=%#v", report.Failed, report.Cases)
	}
	if report.FalsePos != 0 {
		t.Fatalf("false positives=%d", report.FalsePos)
	}
	if report.FalseNeg != 0 {
		t.Fatalf("false negatives=%d", report.FalseNeg)
	}
}
