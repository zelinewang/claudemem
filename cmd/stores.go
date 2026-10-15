package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// readStoresKey is the config key, in the primary store's config.json, that lists the other stores
// its read commands also search (comma-separated; absolute or ~/ paths). Writes always go to the
// primary store, the one given with --store.
const readStoresKey = "stores.read"

// readStoresFlag holds --read-stores, which replaces the stores.read setting for one call; an empty
// value reads the primary store alone.
var readStoresFlag string

func init() {
	rootCmd.PersistentFlags().StringVar(&readStoresFlag, "read-stores", "",
		"other stores that reads also return (comma-separated; default: the "+readStoresKey+" setting; \"\" for none); writes go to --store only")
}

// readStore is one store a read command searches: the primary store (where writes go) or one of the
// stores it reads.
type readStore struct {
	Name  string // the directory as shown to people, the home directory written as ~
	Dir   string
	Store *storage.FileStore
}

// displayStoreName writes dir with the home directory as ~, so output names a store without the
// absolute home path.
func displayStoreName(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return dir
}

// expandStorePath turns an absolute or ~/ store path into a clean absolute path.
func expandStorePath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", p, err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("store path %q must be absolute or start with ~/", p)
	}
	return filepath.Clean(p), nil
}

// primaryStoreDir is the --store directory as a clean absolute path.
func primaryStoreDir() (string, error) {
	dir, err := filepath.Abs(getStoreDir())
	if err != nil {
		return "", fmt.Errorf("resolve the store directory: %w", err)
	}
	return filepath.Clean(dir), nil
}

// readStoreDirs returns the other stores to read besides primary: --read-stores when given, else the
// primary's stores.read setting (only the primary's: the stores it names are not followed further).
// The primary itself and repeats are dropped, also when two paths reach one directory by a symlink.
func readStoreDirs(primary string) ([]string, error) {
	list := readStoresFlag
	if f := rootCmd.PersistentFlags().Lookup("read-stores"); f == nil || !f.Changed {
		cfg, err := config.Load(primary)
		if err != nil {
			return nil, err
		}
		list = cfg.GetString(readStoresKey)
	}
	seen := map[string]bool{sameDirKey(primary): true}
	var dirs []string
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		dir, err := expandStorePath(item)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", readStoresKey, err)
		}
		if key := sameDirKey(dir); !seen[key] {
			seen[key] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// sameDirKey is dir with symlinks resolved, or dir itself when it cannot be resolved (for example when
// it does not exist).
func sameDirKey(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// openReadStores opens the primary store and then each store it reads, in that order. A store it
// reads that does not exist, or is not a claudemem store, is skipped with a warning on stderr (and
// left as it is): read commands go on with the rest.
// The stores it reads record no access log, so reading them writes nothing there. Vector search is
// not set up here; search does that per store.
func openReadStores() ([]readStore, error) {
	primary, err := primaryStoreDir()
	if err != nil {
		return nil, err
	}
	others, err := readStoreDirs(primary)
	if err != nil {
		return nil, err
	}
	first, err := storage.NewFileStore(primary)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}
	stores := []readStore{{Name: displayStoreName(primary), Dir: primary, Store: first}}
	for _, dir := range others {
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "warning: store %s (%s) not found; reading without it\n", displayStoreName(dir), readStoresKey)
			continue
		}
		if !storage.IsStoreDir(dir) {
			fmt.Fprintf(os.Stderr, "warning: %s (%s) is not a claudemem store (no .index/search.db); reading without it\n", displayStoreName(dir), readStoresKey)
			continue
		}
		fs, err := storage.NewFileStore(dir)
		if err != nil {
			closeReadStores(stores)
			return nil, fmt.Errorf("open store %s: %w", displayStoreName(dir), err)
		}
		fs.DisableAccessLog()
		stores = append(stores, readStore{Name: displayStoreName(dir), Dir: dir, Store: fs})
	}
	return stores, nil
}

func closeReadStores(stores []readStore) {
	for _, s := range stores {
		s.Store.Close()
	}
}

// readStoreHint adds to a write command's error, when the entry is missing from the primary store
// but sits in a store the primary only reads, where it is: writes go to --store alone, so the command
// has to be run again with that store. Any other error is returned as it is.
func readStoreHint(err error, kind, id string) error {
	if err == nil || !strings.Contains(err.Error(), "not found") {
		return err
	}
	primary, perr := primaryStoreDir()
	if perr != nil {
		return err
	}
	dirs, derr := readStoreDirs(primary)
	if derr != nil {
		return err
	}
	for _, dir := range dirs {
		if !storage.IsStoreDir(dir) {
			continue
		}
		fs, openErr := storage.NewFileStore(dir)
		if openErr != nil {
			continue
		}
		var getErr error
		if kind == "session" {
			_, getErr = fs.GetSession(id)
		} else {
			_, getErr = fs.GetNote(id)
		}
		fs.Close()
		if getErr == nil {
			name := displayStoreName(dir)
			return fmt.Errorf("%w\n  the %s is in %s, which this store only reads; writes go to --store alone, so run the command again with --store %s", err, kind, name, name)
		}
	}
	return err
}

// storeLabel is the store name to show next to an entry: empty when only one store is read, so the
// output of a single store stays as it always was.
func storeLabel(stores []readStore, i int) string {
	if len(stores) < 2 {
		return ""
	}
	return stores[i].Name
}

// interleave merges ranked lists, one from each store, by rank: the first of each list (in store
// order), then the second of each, and so on. Scores from different stores are not comparable (each
// index ranks against its own corpus), ranks are.
func interleave[T any](lists [][]T) []T {
	var out []T
	for rank := 0; ; rank++ {
		added := false
		for _, list := range lists {
			if rank < len(list) {
				out = append(out, list[rank])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}
