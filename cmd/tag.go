package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	tagApplyFile   string
	tagApplyDryRun bool
)

var tagCmd = &cobra.Command{
	Use:   "tag",
	Short: "Edit tags across many notes and sessions",
	Long: `Edit tags across many notes and sessions. For one entry use
"note update <id> --add-tag/--remove-tag" or "session update <id> --add-tag/--remove-tag".`,
}

var tagApplyCmd = &cobra.Command{
	Use:   "apply --file <edits.jsonl>",
	Short: "Add and remove tags on many notes and sessions from a JSONL file",
	Long: `Add and remove tags on many notes and sessions in one run.

Each line of the file is one JSON object:

  {"id": "<full id>", "kind": "note", "add": ["vis-shared"], "remove": ["vis-personal"]}

kind is "note" or "session"; ids are full ids, never prefixes. The whole file is
checked first: an unknown field, a bad kind, an invalid tag, a tag both added and
removed, or an entry listed twice stops the run before anything changes.

Each entry is then edited on its own, atomically and under the store lock: the
tags list in its file's frontmatter and its index rows change together, and
nothing else does (not the body, the timestamps or the vectors). An unknown id
is reported and the run goes on. Running the same file again changes nothing.
--dry-run reports the same counts without writing. "--file -" reads stdin.

Exits 1 when an id is unknown or an entry failed.

Examples:
  claudemem tag apply --file labels.jsonl --dry-run
  claudemem tag apply --file labels.jsonl --format json`,
	Args: cobra.NoArgs,
	RunE: runTagApply,
	// An unknown id or a bad line is a data problem, reported in full; the usage text would bury it
	SilenceUsage: true,
}

func init() {
	tagApplyCmd.Flags().StringVar(&tagApplyFile, "file", "", "JSONL file of tag edits (\"-\" reads stdin)")
	tagApplyCmd.Flags().BoolVar(&tagApplyDryRun, "dry-run", false, "Report what would change without writing")
	tagApplyCmd.MarkFlagRequired("file")
	tagCmd.AddCommand(tagApplyCmd)
	rootCmd.AddCommand(tagCmd)
}

func runTagApply(cmd *cobra.Command, args []string) error {
	var in io.Reader = os.Stdin
	if tagApplyFile != "-" {
		f, err := os.Open(tagApplyFile)
		if err != nil {
			return fmt.Errorf("open tag edits: %w", err)
		}
		defer f.Close()
		in = f
	}
	edits, err := storage.ParseTagEdits(in)
	if err != nil {
		return err
	}

	// No vector store: a tag edit never re-embeds, so the run needs no embedding backend.
	store, err := getFileStore()
	if err != nil {
		return err
	}
	defer store.Close()

	report := store.ApplyTagEdits(edits, tagApplyDryRun, func(done, total int) {
		if done%1000 == 0 && done < total {
			fmt.Fprintf(os.Stderr, "tag apply: %d/%d\n", done, total)
		}
	})

	if outputFormat == "json" {
		if err := OutputJSON(report); err != nil {
			return err
		}
	} else {
		printTagApplyReport(report)
	}
	if len(report.Unknown) > 0 || len(report.Failed) > 0 {
		return fmt.Errorf("tag apply: %d unknown ids, %d failed entries", len(report.Unknown), len(report.Failed))
	}
	return nil
}

func printTagApplyReport(r *storage.TagApplyReport) {
	header, verb := "tag apply", "changed"
	if r.DryRun {
		header, verb = "tag apply (dry run)", "would change"
	}
	OutputText("%s: %d entries, %d %s (%d notes, %d sessions), %d unchanged, %d unknown, %d failed",
		header, r.Entries, r.Changed, verb, r.NotesChanged, r.SessionsChanged, r.Unchanged, len(r.Unknown), len(r.Failed))
	for _, issue := range r.Unknown {
		OutputText("  unknown, line %d: %s", issue.Line, issue.Reason)
	}
	for _, issue := range r.Failed {
		OutputText("  failed, line %d: %s", issue.Line, issue.Reason)
	}
}

// splitTagList splits a comma-separated tag list, trimming spaces and dropping empty items.
func splitTagList(s string) []string {
	var tags []string
	for _, tag := range strings.Split(s, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// printTagEdit reports a single tag edit (note update / session update --add-tag/--remove-tag).
func printTagEdit(res *storage.TagEditResult) error {
	if outputFormat == "json" {
		return OutputJSON(res)
	}
	idShort := res.ID
	if len(idShort) > 8 {
		idShort = idShort[:8]
	}
	tags := strings.Join(res.After, ", ")
	if tags == "" {
		tags = "(none)"
	}
	if res.Changed {
		OutputText("✓ Updated tags of %s \"%s\" (id: %s): %s", res.Kind, res.Title, idShort, tags)
	} else {
		OutputText("Tags of %s \"%s\" (id: %s) already as requested: %s", res.Kind, res.Title, idShort, tags)
	}
	return nil
}
