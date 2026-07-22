package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── claudemem session repair (CLI level) ───────────────────────────────────
//
// Safety contract pinned here:
//   - dry-run is the DEFAULT: full report, zero filesystem writes
//   - --apply REQUIRES --backup-dir: refusal is non-zero-exit + zero writes
//   - backups hold pre-repair originals; mode-A / healthy files byte-preserved
//   - a second --apply is a no-op (idempotent)

const repairCLI_fixtureFused = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-cli-a
project: moa2-cli
session_id: sid-cli-a
tags: [moa2]
title: cli fused
type: session
---

## Summary

CLI fixture for session repair.

## Problems & Solutions
- **Problem**: CLI-P1 fused body **Solution**: CLI-S1 recovered body
  **Solution**:

## Next Steps

- [ ] CLI-NEXT kept
`

const repairCLI_fixtureHealthy = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-cli-e
project: moa2-cli
session_id: sid-cli-e
tags: [moa2]
title: cli healthy
type: session
---

## Summary

CLI fixture healthy session.

## Problems & Solutions
- **Problem**: CLI-E1 healthy
  **Solution**: CLI-E1-SOL healthy
`

// setupSessionRepairStore seeds an isolated store dir with the given session
// files, redirects the package-level store/flag state, and restores it on
// cleanup. Returns (storePath, sessionsDir).
func setupSessionRepairStore(t *testing.T, files map[string]string) (string, string) {
	t.Helper()

	storePath := t.TempDir()
	sessionsDir := filepath.Join(storePath, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(sessionsDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	origStoreDir := storeDir
	origApply, origBackup := sessionRepairApply, sessionRepairBackupDir
	storeDir = storePath
	sessionRepairApply = false
	sessionRepairBackupDir = ""
	t.Cleanup(func() {
		storeDir = origStoreDir
		sessionRepairApply = origApply
		sessionRepairBackupDir = origBackup
	})
	return storePath, sessionsDir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestSessionRepair_DryRunIsDefaultAndWritesNothing(t *testing.T) {
	_, sessionsDir := setupSessionRepairStore(t, map[string]string{
		"fused.md":   repairCLI_fixtureFused,
		"healthy.md": repairCLI_fixtureHealthy,
	})
	backupDir := filepath.Join(filepath.Dir(sessionsDir), "backups")

	// Default flags (no --apply): run and capture output.
	out := captureStdout(t, func() {
		if err := runSessionRepair(nil, nil); err != nil {
			t.Fatalf("dry-run returned error: %v", err)
		}
	})

	if got := readFile(t, filepath.Join(sessionsDir, "fused.md")); got != repairCLI_fixtureFused {
		t.Error("dry-run modified the fused session file")
	}
	if got := readFile(t, filepath.Join(sessionsDir, "healthy.md")); got != repairCLI_fixtureHealthy {
		t.Error("dry-run modified the healthy session file")
	}
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Error("dry-run must not create a backup directory")
	}
	if !strings.Contains(out, "fused.md") {
		t.Error("dry-run report must name the repairable file")
	}
	if !strings.Contains(out, "CLI-P1") || !strings.Contains(out, "CLI-S1") {
		t.Error("dry-run report must show before/after preview content")
	}
}

func TestSessionRepair_ApplyWithoutBackupDirRefused(t *testing.T) {
	_, sessionsDir := setupSessionRepairStore(t, map[string]string{
		"fused.md": repairCLI_fixtureFused,
	})

	sessionRepairApply = true
	sessionRepairBackupDir = ""
	err := runSessionRepair(nil, nil)
	if err == nil {
		t.Fatal("--apply without --backup-dir must fail")
	}
	if got := readFile(t, filepath.Join(sessionsDir, "fused.md")); got != repairCLI_fixtureFused {
		t.Error("refused apply must write nothing")
	}
}

func TestSessionRepair_BackupDirInsideSessionsRefused(t *testing.T) {
	_, sessionsDir := setupSessionRepairStore(t, map[string]string{
		"fused.md": repairCLI_fixtureFused,
	})

	sessionRepairApply = true
	sessionRepairBackupDir = filepath.Join(sessionsDir, "backups")
	if err := runSessionRepair(nil, nil); err == nil {
		t.Fatal("--backup-dir inside the sessions dir must be refused")
	}
	if got := readFile(t, filepath.Join(sessionsDir, "fused.md")); got != repairCLI_fixtureFused {
		t.Error("refused apply must write nothing")
	}
}

func TestSessionRepair_ApplyRepairsAndBacksUp(t *testing.T) {
	storePath, sessionsDir := setupSessionRepairStore(t, map[string]string{
		"fused.md":   repairCLI_fixtureFused,
		"healthy.md": repairCLI_fixtureHealthy,
	})
	backupDir := filepath.Join(storePath, "backups")

	sessionRepairApply = true
	sessionRepairBackupDir = backupDir
	if err := runSessionRepair(nil, nil); err != nil {
		t.Fatalf("apply returned error: %v", err)
	}

	repaired := readFile(t, filepath.Join(sessionsDir, "fused.md"))
	if !strings.Contains(repaired, "- **Problem**: CLI-P1 fused body\n  **Solution**: CLI-S1 recovered body") {
		t.Errorf("fused line not split into canonical pair:\n%s", repaired)
	}
	if strings.Contains(repaired, "CLI-P1 fused body **Solution**:") {
		t.Error("fused residue still present after apply")
	}
	if strings.Count(repaired, "**Solution**:") != 1 {
		t.Errorf("orphan empty marker should be consumed, file:\n%s", repaired)
	}
	if !strings.Contains(repaired, "CLI-NEXT kept") {
		t.Error("other sections must survive repair")
	}
	if got := readFile(t, filepath.Join(sessionsDir, "healthy.md")); got != repairCLI_fixtureHealthy {
		t.Error("healthy file must be byte-preserved")
	}

	// Backup holds the pre-repair original.
	backed := readFile(t, filepath.Join(backupDir, "fused.md"))
	if backed != repairCLI_fixtureFused {
		t.Error("backup must contain the pre-repair original bytes")
	}
	if _, err := os.Stat(filepath.Join(backupDir, "healthy.md")); !os.IsNotExist(err) {
		t.Error("unchanged files should not be backed up")
	}
}

func TestSessionRepair_SecondApplyIsNoOp(t *testing.T) {
	storePath, sessionsDir := setupSessionRepairStore(t, map[string]string{
		"fused.md": repairCLI_fixtureFused,
	})

	sessionRepairApply = true
	sessionRepairBackupDir = filepath.Join(storePath, "backups1")
	if err := runSessionRepair(nil, nil); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	afterFirst := readFile(t, filepath.Join(sessionsDir, "fused.md"))

	sessionRepairBackupDir = filepath.Join(storePath, "backups2")
	if err := runSessionRepair(nil, nil); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if got := readFile(t, filepath.Join(sessionsDir, "fused.md")); got != afterFirst {
		t.Error("second apply changed bytes (not idempotent)")
	}
	if _, err := os.Stat(filepath.Join(storePath, "backups2")); !os.IsNotExist(err) {
		t.Error("second apply with zero changes must not create a backup dir")
	}
}

// captureStdout swaps os.Stdout for a pipe while fn runs and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = r
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}
