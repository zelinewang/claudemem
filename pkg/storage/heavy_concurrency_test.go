package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// Review of PR #21 round 4, P3-1: the two-worker test cannot see a loosened adoption guard
// (`ours = true`, adopt on ANY re-offer) — that loss only appears above roughly eight concurrent
// writers (reviewer's measurement: 0 shared paths at baseline vs 3/14/13 at 8/16/32 workers with the
// guard loosened). Adapted from the reviewer's TestR3_HeavyConcurrentAddSameSlug. Titles are single
// words so the fuzzy dedup layer does not merge them first; a merge (same id) is still fine.
func TestAddNote_HeavyConcurrentSameSlugNeverLosesAFile(t *testing.T) {
	for _, workers := range []int{8, 16, 32} {
		t.Run(fmt.Sprintf("workers-%d", workers), func(t *testing.T) {
			const iterations = 10
			var totalErrs, totalShared, totalWrongOwner int
			var errSamples []string
			for i := 0; i < iterations; i++ {
				store := setupTestStore(t)
				var wg sync.WaitGroup
				var mu sync.Mutex
				for w := 0; w < workers; w++ {
					wg.Add(1)
					go func(w int) {
						defer wg.Done()
						_, err := store.AddNote(models.NewNote("infrastructure", "Racing", fmt.Sprintf("BODY-%d", w)))
						if err != nil {
							mu.Lock()
							totalErrs++
							if len(errSamples) < 3 {
								errSamples = append(errSamples, err.Error())
							}
							mu.Unlock()
						}
					}(w)
				}
				wg.Wait()

				rows, err := store.db.Query(`SELECT id, filepath FROM entries WHERE type = 'note'`)
				if err != nil {
					t.Fatalf("iter %d: query rows: %v", i, err)
				}
				byPath := map[string]int{}
				var owners []struct{ id, fp string }
				for rows.Next() {
					var id, fp string
					if err := rows.Scan(&id, &fp); err != nil {
						t.Fatalf("scan: %v", err)
					}
					byPath[fp]++
					owners = append(owners, struct{ id, fp string }{id, fp})
				}
				rows.Close()
				for _, o := range owners {
					body, rerr := os.ReadFile(filepath.Join(store.baseDir, o.fp))
					if rerr != nil || !strings.Contains(string(body), o.id) {
						totalWrongOwner++
					}
				}
				for _, n := range byPath {
					if n > 1 {
						totalShared++
					}
				}
				store.Close()
			}
			for _, s := range errSamples {
				t.Logf("  error sample: %s", s)
			}
			if totalErrs > 0 || totalShared > 0 || totalWrongOwner > 0 {
				t.Fatalf("%d-way concurrency: %d AddNote errors, %d shared paths, %d files not owned by their row",
					workers, totalErrs, totalShared, totalWrongOwner)
			}
		})
	}
}
