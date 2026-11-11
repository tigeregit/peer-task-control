package resource

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/retention"
)

// DeleteLocal removes every local representation of a digest. The caller must
// hold retention.Lock, and must have obtained a durable remote keep promise.
func DeleteLocal(packages, casDir, digest string) error {
	if Normalize(digest) == "" {
		return fmt.Errorf("invalid digest")
	}
	held, err := retention.Held(filepath.Dir(packages), digest)
	if err != nil {
		return err
	}
	if held {
		return fmt.Errorf("resource %s is retained for another node", digest)
	}
	entries, err := os.ReadDir(packages)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(packages, entry.Name())
		if s, ok := loadPackage(path); ok && s.Digest == digest {
			if s.Pin {
				return fmt.Errorf("resource %s is pinned", digest)
			}
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if err := SetCID(path, ""); err != nil {
				return err
			}
		}
	}
	path := cas.New(casDir).Path(digest)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return SetCID(path, "")
}
