// Package shiptest has in-memory stand-ins for the shipping module's
// dependencies (orders, object storage), shared by the module's tests.
// Jobs come from testkit, the inbox from webhookstest.
package shiptest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// NewOrders returns an empty order store.
func NewOrders() *Orders { return &Orders{orders: map[ulid.ID]*app.OrderInfo{}} }

// NewObjects returns an empty object store.
func NewObjects() *Objects { return &Objects{M: map[string][]byte{}} }

// Orders keeps orders in memory and moves them only along the
// edges a carrier makes; moves happen when the caller's tx commits.
type Orders struct {
	mu     sync.Mutex
	orders map[ulid.ID]*app.OrderInfo
	Moves  []string // "<order> <to> <actor>"
	Refuse bool     // refuse every move (the merchant cancelled the order)
}

// Add stores an order in status and returns its ID.
func (o *Orders) Add(t *testing.T, status string) ulid.ID {
	t.Helper()
	cod, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	id := ulid.NewGenerator().New()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.orders[id] = &app.OrderInfo{ID: id, Status: status, Name: "منى أحمد", Phone: "+201012345678", Governorate: "EG-C",
		City: "مدينة نصر", Address: "12 شارع عباس العقاد", Notes: "اتصل قبل الوصول", COD: cod, Items: 2, Description: "عطر × 2"}
	return id
}

// Order implements app.Orders.
func (o *Orders) Order(_ context.Context, id ulid.ID) (app.OrderInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	info, ok := o.orders[id]
	if !ok {
		return app.OrderInfo{}, app.ErrOrderUnknown
	}
	return *info, nil
}

// MoveTx implements app.Orders.
func (o *Orders) MoveTx(_ context.Context, _ pgx.Tx, id ulid.ID, to, actor, _ string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Refuse {
		return false, app.ErrOrderMoveRefused
	}
	info := o.orders[id]
	if info.Status == to {
		return false, nil
	}
	info.Status = to
	o.Moves = append(o.Moves, id.String()[20:]+" "+to+" "+actor)
	return true, nil
}

// Status is an order's current status.
func (o *Orders) Status(id ulid.ID) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.orders[id].Status
}

// Objects is an in-memory object store.
type Objects struct {
	mu sync.Mutex
	M  map[string][]byte
}

// PresignUpload implements media.Objects (unused by shipping).
func (o *Objects) PresignUpload(context.Context, string, string, int64, time.Duration) (media.Upload, error) {
	return media.Upload{}, errors.New("not used")
}

// Get implements media.Objects.
func (o *Objects) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.M[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return b, nil
}

// Put implements media.Objects.
func (o *Objects) Put(_ context.Context, key, _ string, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.M[key] = data
	return nil
}

// Delete implements media.Objects.
func (o *Objects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.M, key)
	return nil
}
