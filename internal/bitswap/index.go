package bitswap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"

	"github.com/ipfs/go-cid"
)

const (
	indexFanout    = 256
	maxIndexHeight = 8
)

type indexLink struct {
	CID  string `json:"cid"`
	Size int64  `json:"size"`
}

type indexPage struct {
	Height int         `json:"height"`
	Size   int64       `json:"size"`
	Links  []indexLink `json:"links"`
}

// Each level holds at most one unfinished page, independent of resource size.
type indexBuilder struct {
	exchange *Exchange
	levels   [][]indexLink
}

func (b *indexBuilder) add(ctx context.Context, height int, link indexLink) error {
	if height > maxIndexHeight {
		return fmt.Errorf("resource index exceeds maximum height")
	}
	for len(b.levels) <= height {
		b.levels = append(b.levels, nil)
	}
	b.levels[height] = append(b.levels[height], link)
	if len(b.levels[height]) < indexFanout {
		return nil
	}
	parent, err := b.page(ctx, height)
	if err != nil {
		return err
	}
	return b.add(ctx, height+1, parent)
}

func (b *indexBuilder) page(ctx context.Context, height int) (indexLink, error) {
	page := indexPage{Height: height, Links: b.levels[height]}
	for _, link := range page.Links {
		if link.Size <= 0 || link.Size > math.MaxInt64-page.Size {
			return indexLink{}, fmt.Errorf("invalid resource index size")
		}
		page.Size += link.Size
	}
	data, err := json.Marshal(page)
	if err != nil {
		return indexLink{}, err
	}
	c, err := b.exchange.put(ctx, data)
	if err != nil {
		return indexLink{}, err
	}
	b.levels[height] = nil
	return indexLink{CID: c.String(), Size: page.Size}, nil
}

func (b *indexBuilder) finish(ctx context.Context) (indexLink, int, error) {
	for height := 0; height < len(b.levels); height++ {
		if len(b.levels[height]) == 0 {
			continue
		}
		if height > 0 && height == len(b.levels)-1 && len(b.levels[height]) == 1 {
			return b.levels[height][0], height - 1, nil
		}
		root, err := b.page(ctx, height)
		if err != nil {
			return indexLink{}, 0, err
		}
		if height == len(b.levels)-1 {
			return root, height, nil
		}
		if err := b.add(ctx, height+1, root); err != nil {
			return indexLink{}, 0, err
		}
	}
	return indexLink{}, 0, fmt.Errorf("empty resource index")
}

func indexCapacity(height int) int64 {
	capacity := int64(ChunkSize)
	for i := 0; i <= height; i++ {
		if capacity > math.MaxInt64/indexFanout {
			return math.MaxInt64
		}
		capacity *= indexFanout
	}
	return capacity
}

func (e *Exchange) fetchIndex(ctx context.Context, link indexLink, height int, w io.Writer) error {
	c, err := cid.Decode(link.CID)
	if err != nil {
		return err
	}
	block, err := e.GetBlock(ctx, c)
	if err != nil {
		return err
	}
	var page indexPage
	if err := json.Unmarshal(block.RawData(), &page); err != nil {
		return err
	}
	if page.Height != height || page.Size != link.Size || len(page.Links) == 0 || len(page.Links) > indexFanout {
		return fmt.Errorf("invalid resource index page")
	}
	capacity := int64(ChunkSize)
	if height > 0 {
		capacity = indexCapacity(height - 1)
	}
	var total int64
	for i, child := range page.Links {
		if child.Size <= 0 || child.Size > capacity || child.Size > page.Size-total ||
			(i < len(page.Links)-1 && child.Size != capacity) {
			return fmt.Errorf("invalid resource index child size")
		}
		total += child.Size
	}
	if total != page.Size {
		return fmt.Errorf("resource index size mismatch")
	}
	for _, child := range page.Links {
		if height == 0 {
			err = e.writeChunk(ctx, child, w)
		} else {
			err = e.fetchIndex(ctx, child, height-1, w)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Exchange) writeChunk(ctx context.Context, link indexLink, w io.Writer) error {
	c, err := cid.Decode(link.CID)
	if err != nil {
		return err
	}
	block, err := e.GetBlock(ctx, c)
	if err != nil {
		return err
	}
	if int64(len(block.RawData())) != link.Size {
		return fmt.Errorf("invalid transport chunk size")
	}
	if n, err := w.Write(block.RawData()); err != nil {
		return err
	} else if n != len(block.RawData()) {
		return io.ErrShortWrite
	}
	return nil
}
