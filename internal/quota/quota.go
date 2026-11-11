package quota

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const GB int64 = 1_000_000_000

var ErrExceeded = errors.New("storage admission refused")

type Bytes int64

func (b *Bytes) UnmarshalYAML(n *yaml.Node) error {
	s := strings.ToUpper(strings.TrimSpace(n.Value))
	mult := int64(1)
	for _, unit := range []struct {
		name string
		size int64
	}{{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10}, {"GB", GB}, {"MB", 1_000_000}, {"KB", 1_000}, {"B", 1}} {
		if strings.HasSuffix(s, unit.name) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, unit.name)), unit.size
			break
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 || v > math.MaxInt64/mult {
		return fmt.Errorf("invalid byte size %q (use integer bytes, MB, GB or GiB)", n.Value)
	}
	*b = Bytes(v * mult)
	return nil
}

func Adjust(limit, deleteSize, free int64) (int64, int64) {
	configured := limit
	if free < limit {
		limit = max(0, free-GB)
	}
	if limit == 0 {
		return 0, 0
	}
	n := new(big.Int).Mul(big.NewInt(deleteSize), big.NewInt(limit))
	n.Quo(n, big.NewInt(configured))
	return limit, n.Int64()
}

func Size(root string) (int64, error) {
	return measure(root, true)
}

func WorkspaceUsage(root string) (int64, error) {
	return measure(root, false)
}

func measure(root string, strict bool) (int64, error) {
	var size int64
	err := filepath.Walk(root, func(_ string, st os.FileInfo, err error) error {
		if os.IsNotExist(err) {
			return nil
		}

		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			if !strict {
				return nil
			}
			return fmt.Errorf("storage must not contain symlinks: %s", root)
		}
		if st.Mode().IsRegular() {
			if st.Size() > math.MaxInt64-size {
				return fmt.Errorf("storage size overflow")
			}
			size += st.Size()
		}
		return nil
	})
	return size, err
}

func SeedUsage(root string) (int64, error) {
	var total int64
	for _, name := range []string{"packages", "cas", "blocks", ".fetch"} {
		n, err := Size(filepath.Join(root, name))
		if err != nil {
			return 0, err
		}
		if n > math.MaxInt64-total {
			return 0, fmt.Errorf("storage size overflow")
		}
		total += n
	}
	return total, nil
}
