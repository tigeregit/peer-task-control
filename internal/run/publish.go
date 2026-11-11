package run

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

// PublishLocal applies the same storage admission to large dispatch inputs
// published on the submitter before the bundle is sent.
func PublishLocal(ctx context.Context, name, src string, opt Options) (seed resource.Seed, retErr error) {
	opt.Context = ctx
	opt.skipSeeds = map[string]bool{}
	var err error
	opt.RunID, err = TaskID(time.Now(), "publish")
	if err != nil {
		return seed, err
	}
	src, err = filepath.Abs(src)
	if err != nil {
		return seed, err
	}
	loaded := workflow.Loaded{SeedOnly: true, Prepare: workflow.Prepare{Seeds: []workflow.Seed{
		{Name: name, Local: &workflow.LocalSeed{Src: src}},
	}}}
	_, release, err := opt.reserveStorage(loaded, "", "")
	if err != nil {
		return seed, err
	}
	defer func() { retErr = errors.Join(retErr, release()) }()
	seed, retErr = resource.PublishTreeContext(ctx, opt.packagesDir(), name, src, time.Now())
	if retErr == nil {
		retErr = opt.checkStorage()
	}
	return seed, retErr
}
