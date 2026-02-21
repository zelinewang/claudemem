package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export [output-file]",
	Short: "Export all data as a portable tar.gz archive",
	Long: `Export the entire claudemem store as a tar.gz archive for backup or migration.
The archive includes all notes, sessions, and configuration (excluding the SQLite index,
which is rebuilt automatically on import).

Examples:
  claudemem export                          # Creates claudemem-backup-2026-02-21.tar.gz
  claudemem export my-backup.tar.gz         # Custom filename
  claudemem export ~/backups/memory.tar.gz  # Custom path`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Determine output filename
		outFile := ""
		if len(args) > 0 {
			outFile = args[0]
		} else {
			outFile = fmt.Sprintf("claudemem-backup-%s.tar.gz", time.Now().Format("2006-01-02"))
		}

		// Open output file
		f, err := os.Create(outFile)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer f.Close()

		gz := gzip.NewWriter(f)
		defer gz.Close()
		tw := tar.NewWriter(gz)
		defer tw.Close()

		// Walk the store directory, skip .index/ (SQLite is rebuilt)
		fileCount := 0
		err = filepath.Walk(storeDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			rel, _ := filepath.Rel(storeDir, path)

			// Skip .index directory (SQLite index — rebuilt on import)
			if strings.HasPrefix(rel, ".index") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Create tar header
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return nil
			}
			header.Name = filepath.Join("claudemem", rel)

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if !info.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				if _, err := tw.Write(data); err != nil {
					return err
				}
				fileCount++
			}

			return nil
		})

		if err != nil {
			return fmt.Errorf("export failed: %w", err)
		}

		if outputFormat == "json" {
			return OutputJSON(map[string]interface{}{
				"file":       outFile,
				"file_count": fileCount,
			})
		}

		OutputText("✓ Exported %d files to %s", fileCount, outFile)
		OutputText("  Restore with: claudemem import %s", outFile)
		return nil
	},
}

var importCmd = &cobra.Command{
	Use:   "import <archive-file>",
	Short: "Import data from a claudemem export archive",
	Long: `Import data from a tar.gz archive created by 'claudemem export'.
Extracts notes, sessions, and configuration to the store directory.
The SQLite FTS index is rebuilt automatically after import.

Examples:
  claudemem import claudemem-backup-2026-02-21.tar.gz
  claudemem import ~/backups/memory.tar.gz`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		archiveFile := args[0]

		f, err := os.Open(archiveFile)
		if err != nil {
			return fmt.Errorf("failed to open archive: %w", err)
		}
		defer f.Close()

		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("failed to read gzip: %w", err)
		}
		defer gz.Close()

		tr := tar.NewReader(gz)
		fileCount := 0

		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read tar: %w", err)
			}

			// Strip the "claudemem/" prefix from archive paths
			name := header.Name
			if strings.HasPrefix(name, "claudemem/") {
				name = strings.TrimPrefix(name, "claudemem/")
			}
			if name == "" || name == "." {
				continue
			}

			targetPath := filepath.Join(storeDir, name)

			// Security: ensure path doesn't escape store directory
			absTarget, _ := filepath.Abs(targetPath)
			absStore, _ := filepath.Abs(storeDir)
			if !strings.HasPrefix(absTarget, absStore) {
				continue // skip paths that escape the store
			}

			switch header.Typeflag {
			case tar.TypeDir:
				os.MkdirAll(targetPath, 0700)
			case tar.TypeReg:
				os.MkdirAll(filepath.Dir(targetPath), 0700)
				outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
				if err != nil {
					continue
				}
				io.Copy(outFile, tr)
				outFile.Close()
				fileCount++
			}
		}

		// Rebuild the SQLite index from imported markdown files
		OutputText("Rebuilding search index...")
		store, err := getSessionStore()
		if err != nil {
			return fmt.Errorf("failed to open store for reindexing: %w", err)
		}
		defer store.Close()

		indexed, reindexErr := store.Reindex()
		if reindexErr != nil {
			OutputText("⚠ Reindex warning: %v", reindexErr)
		} else {
			OutputText("  Indexed %d entries", indexed)
		}

		if outputFormat == "json" {
			return OutputJSON(map[string]interface{}{
				"file":       archiveFile,
				"file_count": fileCount,
			})
		}

		OutputText("✓ Imported %d files from %s", fileCount, archiveFile)
		OutputText("  Run 'claudemem stats' to verify.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(importCmd)
}
