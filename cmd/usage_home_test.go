package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cliChildArgs carries the CLI arguments (separated by \x1f) into a child process started by runCLIChild.
const cliChildArgs = "CLAUDEMEM_TEST_CLI_ARGS"

// TestMain lets this test binary act as the CLI in a child process. Flag defaults are set when the
// package initialises, so a test that needs another HOME has to run the CLI in a fresh process.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(cliChildArgs); ok {
		rootCmd.SetArgs(strings.Split(args, "\x1f"))
		Execute() // exits 1 after printing the error and the usage
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runCLIChild runs the CLI in a child process with HOME set to home and returns stdout and stderr together.
func runCLIChild(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), "HOME="+home, cliChildArgs+"="+strings.Join(args, "\x1f"))
	out, err := child.CombinedOutput()
	return string(out), err
}

// Help and the usage block cobra prints after a command error must not show the absolute home path:
// agents read this output, and their transcripts can reach places other people read.
func TestUsage_NeverPrintsTheHomePath(t *testing.T) {
	home := t.TempDir()
	store := t.TempDir()
	for _, args := range [][]string{
		{"--help"},
		{"note", "get", "--help"},
		{"--store", store, "note", "get", "no-such-id"},     // an error from the command itself
		{"--store", store, "note", "get", "--no-such-flag"}, // a flag error
	} {
		out, _ := runCLIChild(t, home, args...)
		if !strings.Contains(out, "--store") {
			t.Fatalf("%v: no usage block in the output:\n%s", args, out)
		}
		if strings.Contains(out, home) {
			t.Errorf("%v printed the home path %s:\n%s", args, home, out)
		}
	}
}

// Without --store every command uses ~/.claudemem; export and import read the store directory
// themselves, so they are checked here.
func TestExportImport_DefaultStoreIsHomeClaudemem(t *testing.T) {
	home := t.TempDir()
	if out, err := runCLIChild(t, home, "note", "add", "work", "--title", "Exported note", "--content", "kept"); err != nil {
		t.Fatalf("note add: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claudemem", "notes", "work")); err != nil {
		t.Fatalf("note add without --store did not write to ~/.claudemem: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if out, err := runCLIChild(t, home, "export", archive); err != nil || !strings.Contains(out, "Exported") {
		t.Fatalf("export: %v\n%s", err, out)
	}

	other := t.TempDir()
	if out, err := runCLIChild(t, other, "import", archive); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	out, err := runCLIChild(t, other, "note", "get", "work", "Exported note")
	if err != nil || !strings.Contains(out, "kept") {
		t.Fatalf("the imported note is not in the other ~/.claudemem: %v\n%s", err, out)
	}
}
