package bitswap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ipfs/go-cid"
)

// Prune marks all retained v1/v2 roots before sweeping. Missing/corrupt indexes
// abort the sweep, so incomplete metadata cannot erase another resource's cache.
func (e *Exchange) Prune(ctx context.Context, roots []string) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.operations.Lock()
	defer e.operations.Unlock()
	keep := map[string]bool{}
	var page func(string, int) error
	page = func(raw string, height int) error {
		if keep[raw] {
			return nil
		}
		c, err := cid.Decode(raw)
		if err != nil {
			return err
		}
		block, err := e.store.Get(ctx, c)
		if err != nil {
			return err
		}
		var p indexPage
		if err := json.Unmarshal(block.RawData(), &p); err != nil {
			return err
		}
		if p.Height != height || height < 0 || height > maxIndexHeight || len(p.Links) == 0 || len(p.Links) > indexFanout {
			return fmt.Errorf("invalid cached index page")
		}
		keep[raw] = true
		for _, child := range p.Links {
			if height == 0 {
				if _, err := cid.Decode(child.CID); err != nil {
					return err
				}
				keep[child.CID] = true
			} else if err := page(child.CID, height-1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, raw := range roots {
		c, err := cid.Decode(raw)
		if err != nil {
			return 0, fmt.Errorf("cannot prune an unindexed seed: %w", err)
		}
		block, err := e.store.Get(ctx, c)
		if err != nil {
			return 0, err
		}
		var m Manifest
		if err := json.Unmarshal(block.RawData(), &m); err != nil {
			return 0, err
		}
		keep[raw] = true
		switch m.Version {
		case 1:
			for _, child := range m.Chunks {
				if _, err := cid.Decode(child); err != nil {
					return 0, err
				}
				keep[child] = true
			}
		case 2:
			if err := page(m.Index, m.Height); err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("unsupported retained manifest")
		}
	}
	keys, err := e.store.AllKeysChan(ctx)
	if err != nil {
		return 0, err
	}
	var freed int64
	for c := range keys {
		if keep[c.String()] {
			continue
		}
		size, err := e.store.GetSize(ctx, c)
		if err != nil {
			return freed, err
		}
		if err := e.store.DeleteBlock(ctx, c); err != nil {
			return freed, err
		}
		freed += int64(size)
	}
	return freed, ctx.Err()
}
