package testkit

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// NewJobs returns an empty job capture, a jobs.Inserter for module tests
// that run their jobs by hand.
func NewJobs() *Jobs { return &Jobs{} }

// Jobs keeps the jobs enqueued in committed transactions.
type Jobs struct {
	mu   sync.Mutex
	Args []river.JobArgs
	Opts []*river.InsertOpts
}

// InsertTx implements jobs.Inserter: it keeps the job.
func (c *Jobs) InsertTx(_ context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if tx == nil {
		return nil, errors.New("jobs must be enqueued in a transaction")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Args = append(c.Args, args)
	c.Opts = append(c.Opts, opts)
	return &rivertype.JobInsertResult{}, nil
}

// Kinds lists the kinds of the jobs enqueued so far.
func (c *Jobs) Kinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.Args))
	for i, j := range c.Args {
		out[i] = j.Kind()
	}
	return out
}
