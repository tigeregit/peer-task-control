package bitswap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// Store keeps raw SHA-256 blocks independently of the resource CAS. Checking on
// read prevents a damaged local cache from being served as valid Bitswap data.
type Store struct {
	root string
	mu   sync.Mutex
}

func (s *Store) path(c cid.Cid) string {
	return filepath.Join(s.root, c.String())
}

func checkCID(c cid.Cid) error {
	p := c.Prefix()
	if p.Version != 1 || p.Codec != cid.Raw || p.MhType != 0x12 || p.MhLength != 32 {
		return fmt.Errorf("unsupported block CID %s", c)
	}
	return nil
}

func verify(c cid.Cid, data []byte) error {
	if err := checkCID(c); err != nil {
		return err
	}
	got, err := c.Prefix().Sum(data)
	if err != nil {
		return err
	}
	if !got.Equals(c) {
		return fmt.Errorf("block digest mismatch: expected %s, got %s", c, got)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkCID(c); err != nil {
		return nil, err
	}
	f, err := os.Open(s.path(c))
	if os.IsNotExist(err) {
		return nil, ipld.ErrNotFound{Cid: c}
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBlock(f)
	if err != nil {
		return nil, err
	}
	if err := verify(c, data); err != nil {
		return nil, err
	}
	return blocks.NewBlockWithCid(data, c)
}

func (s *Store) Has(ctx context.Context, c cid.Cid) (bool, error) {
	_, err := s.Get(ctx, c)
	if ipld.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) GetSize(ctx context.Context, c cid.Cid) (int, error) {
	b, err := s.Get(ctx, c)
	if err != nil {
		return 0, err
	}
	return len(b.RawData()), nil
}

func (s *Store) Put(ctx context.Context, b blocks.Block) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(b.RawData()) > maxBlockSize {
		return fmt.Errorf("block exceeds %d bytes", maxBlockSize)
	}
	if err := verify(b.Cid(), b.RawData()); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Blocks are immutable. Replacing an existing block can fail on Windows
	// while a Bitswap reader has it open.
	if _, err := s.Get(ctx, b.Cid()); err == nil {
		return nil
	} else if !ipld.IsNotFound(err) {
		return err
	}
	f, err := os.CreateTemp(s.root, ".block-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b.RawData())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path(b.Cid())); err != nil {
		// Another writer may have committed the same verified block first.
		if _, existingErr := s.Get(ctx, b.Cid()); existingErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func (s *Store) PutMany(ctx context.Context, bs []blocks.Block) error {
	for _, b := range bs {
		if err := s.Put(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteBlock(ctx context.Context, c cid.Cid) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkCID(c); err != nil {
		return err
	}
	err := os.Remove(s.path(c))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := make(chan cid.Cid)
	go func() {
		defer close(out)
		for _, e := range entries {
			c, err := cid.Decode(e.Name())
			if err != nil || e.IsDir() || checkCID(c) != nil {
				continue
			}
			select {
			case out <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
