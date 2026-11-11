// Package worktreeguard coordinates runners and worktree cleaners across processes.
package worktreeguard

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrBusy = errors.New("worktree is active")

func Separate(a, b string) error {
	a, err := canonical(a)
	if err != nil {
		return err
	}
	b, err = canonical(b)
	if err != nil {
		return err
	}
	if contains(a, b) || contains(b, a) {
		return fmt.Errorf("storage paths overlap: %s and %s", a, b)
	}
	return nil
}

func Child(root, child string) error {
	root, err := canonical(root)
	if err != nil {
		return err
	}
	child, err = canonical(child)
	if err != nil {
		return err
	}
	if root == child || !contains(root, child) {
		return fmt.Errorf("workspace %s must be strictly inside %s", child, root)
	}
	return nil
}

const lockDirName = ".ptc-locks"

type Guard struct {
	files []*os.File
}

// TryLock guards a worktree against removal, including when path is an ancestor
// or a descendant of a child of root. Paths outside root need no guard.
func TryLock(root, path string) (*Guard, error) {
	root, err := canonical(root)
	if err != nil {
		return nil, err
	}
	path, err = canonical(path)
	if err != nil {
		return nil, err
	}
	lockDir := filepath.Join(root, lockDirName)
	if contains(lockDir, path) {
		return nil, fmt.Errorf("worktree %s overlaps the reserved lock directory", path)
	}

	rel, err := filepath.Rel(root, path)
	underRoot := err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)))
	rootExclusive := contains(path, root)
	if !underRoot && !rootExclusive {
		return &Guard{}, nil
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("create worktree lock directory: %w", err)
	}
	g := &Guard{}
	if err := g.lock(filepath.Join(lockDir, "root"), !rootExclusive); err != nil {
		return nil, err
	}
	if !rootExclusive {
		child := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		sum := sha256.Sum256([]byte(child))
		if err := g.lock(filepath.Join(lockDir, fmt.Sprintf("%x", sum)), false); err != nil {
			return nil, errors.Join(err, g.Close())
		}
	}
	return g, nil
}

// ContainsLockDir reports whether removing path would remove root's locks.
func ContainsLockDir(root, path string) (bool, error) {
	root, err := canonical(root)
	if err != nil {
		return false, err
	}
	path, err = canonical(path)
	if err != nil {
		return false, err
	}
	return contains(path, filepath.Join(root, lockDirName)), nil
}

func (g *Guard) lock(path string, shared bool) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open worktree lock: %w", err)
	}
	if err := lockFile(f, shared); err != nil {
		return errors.Join(err, f.Close())
	}
	g.files = append(g.files, f)
	return nil
}

func (g *Guard) Close() error {
	var err error
	for i := len(g.files) - 1; i >= 0; i-- {
		err = errors.Join(err, unlockFile(g.files[i]), g.files[i].Close())
	}
	g.files = nil
	return err
}

func canonical(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			if runtime.GOOS == "windows" {
				resolved = strings.ToLower(resolved)
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve worktree path %s: %w", path, err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}

func Same(a, b string) (bool, error) {
	left, err := canonical(a)
	if err != nil {
		return false, err
	}
	right, err := canonical(b)
	if err != nil {
		return false, err
	}
	return left == right, nil
}

func contains(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)))
}
