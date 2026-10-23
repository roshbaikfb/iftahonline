// Package webhookstest has an in-memory webhooks.Inbox for the tests of
// modules whose jobs process webhooks.
package webhookstest

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// NewInbox returns an empty inbox.
func NewInbox() *Inbox {
	return &Inbox{receipts: map[ulid.ID]webhooks.Receipt{}, bodies: map[ulid.ID][]byte{}, outcome: map[ulid.ID]string{}}
}

// Outcome is what processing recorded for a receipt.
func (m *Inbox) Outcome(id ulid.ID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outcome[id]
}

// Inbox is the webhooks inbox in memory.
type Inbox struct {
	mu       sync.Mutex
	receipts map[ulid.ID]webhooks.Receipt
	bodies   map[ulid.ID][]byte
	outcome  map[ulid.ID]string
}

// Get implements webhooks.Inbox.
func (m *Inbox) Get(_ context.Context, id ulid.ID) (webhooks.Receipt, string, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.receipts[id]
	if !ok {
		return webhooks.Receipt{}, "", nil, webhooks.ErrReceiptNotFound
	}
	return r, "application/json", m.bodies[id], nil
}

// MarkProcessedTx implements webhooks.Inbox.
func (m *Inbox) MarkProcessedTx(_ context.Context, _ pgx.Tx, id ulid.ID, outcome string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, done := m.outcome[id]; done {
		return false, nil
	}
	m.outcome[id] = outcome
	return true, nil
}

// Put stores an accepted delivery of provider and returns its receipt ID.
func (m *Inbox) Put(provider string, body []byte) ulid.ID {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := ulid.NewGenerator().New()
	m.receipts[id] = webhooks.Receipt{ID: id, Provider: provider}
	m.bodies[id] = body
	return id
}
