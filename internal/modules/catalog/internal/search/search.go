// Package search is the only code that talks to Meilisearch (depguard
// enforces it). Every store's products share one index; this client adds
// the store filter, taken from tenant.FromContext, to every search and
// every deletion, and refuses to index another store's documents
// (ADR-0013). Callers cannot pass a filter of their own.
package search

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/meilisearch/meilisearch-go"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Doc is one product in the index. search_* fields hold the normalized
// text (domain.NormalizeSearch); the rest is what results display.
type Doc struct {
	ID          string   `json:"id"`
	StoreID     string   `json:"store_id"`
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Status      string   `json:"status"`
	CategoryIDs []string `json:"category_ids"`
	PriceMinor  int64    `json:"price_minor"` // the lowest variant price
	Currency    string   `json:"currency"`
	ImageKey    string   `json:"image_key,omitempty"` // master of the first ready image
	CreatedAt   int64    `json:"created_at"`          // unix seconds
	SearchTitle string   `json:"search_title"`
	SearchText  string   `json:"search_text"` // options, SKUs and description
}

// Query is a search within the store in ctx. Text must already be
// normalized; Status and CategoryID are optional filters.
type Query struct {
	Text       string
	Status     string
	CategoryID *ulid.ID
	Limit      int
	Offset     int
}

// Result is one page of hits.
type Result struct {
	Hits           []Doc
	EstimatedTotal int64
}

// ErrForeignDoc means a document of another store was offered for
// indexing in this store's context.
var ErrForeignDoc = errors.New("search: document belongs to another store")

// Client is the tenant-safe client.
type Client struct {
	ms      meilisearch.ServiceManager
	uid     string
	timeout time.Duration
}

// New returns a client for index uid. timeout bounds each call, waiting
// for indexing tasks included; the HTTP client retries transient
// failures with backoff.
func New(url, apiKey, uid string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ms := meilisearch.New(url, meilisearch.WithAPIKey(apiKey),
		meilisearch.WithCustomRetries([]int{502, 503, 504}, 3))
	return &Client{ms: ms, uid: uid, timeout: timeout}
}

func (c *Client) index() meilisearch.IndexManager { return c.ms.Index(c.uid) }

// storeFilter is the mandatory first clause of every filter.
func storeFilter(ctx context.Context) (string, tenant.StoreID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return "", tenant.StoreID{}, fmt.Errorf("search: %w", err)
	}
	return "store_id = " + quote(id.String()), id, nil
}

// quote makes a filter string literal. Values here are ULIDs and enums,
// validated before they reach us; quote still escapes, so a bug upstream
// cannot turn a value into filter syntax.
func quote(s string) string {
	return strconv.Quote(s)
}

// Settings are the index settings; the worker applies them (EnsureIndex).
func Settings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"search_title", "search_text"},
		FilterableAttributes: []string{"store_id", "id", "status", "category_ids"},
		SortableAttributes:   []string{"price_minor", "created_at"},
		DisplayedAttributes: []string{"id", "store_id", "title", "slug", "status", "category_ids",
			"price_minor", "currency", "image_key", "created_at"},
	}
}

// EnsureIndex creates the index if needed and applies Settings. It is
// idempotent.
func (c *Client) EnsureIndex(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*c.timeout)
	defer cancel()
	task, err := c.ms.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{Uid: c.uid, PrimaryKey: "id"})
	if err != nil {
		return fmt.Errorf("search: create index: %w", err)
	}
	if _, err = c.wait(ctx, task, true); err != nil {
		return err
	}
	task, err = c.index().UpdateSettingsWithContext(ctx, Settings())
	if err != nil {
		return fmt.Errorf("search: settings: %w", err)
	}
	_, err = c.wait(ctx, task, false)
	return err
}

// wait blocks until a task finishes. allowExists accepts an
// index_already_exists failure (EnsureIndex on a second run).
func (c *Client) wait(ctx context.Context, info *meilisearch.TaskInfo, allowExists bool) (*meilisearch.Task, error) {
	task, err := c.ms.WaitForTaskWithContext(ctx, info.TaskUID, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("search: wait for task %d: %w", info.TaskUID, err)
	}
	if task.Status == meilisearch.TaskStatusFailed {
		if allowExists && task.Error.Code == "index_already_exists" {
			return task, nil
		}
		return nil, fmt.Errorf("search: task %d failed: %s: %s", task.UID, task.Error.Code, task.Error.Message)
	}
	return task, nil
}

// Upsert indexes documents of the store in ctx and waits until they are
// searchable. A document of any other store fails the whole call.
func (c *Client) Upsert(ctx context.Context, docs []Doc) error {
	if len(docs) == 0 {
		return nil
	}
	_, store, err := storeFilter(ctx)
	if err != nil {
		return err
	}
	for _, d := range docs {
		if d.StoreID != store.String() {
			return ErrForeignDoc
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	pk := "id"
	task, err := c.index().AddDocumentsWithContext(ctx, docs, &meilisearch.DocumentOptions{PrimaryKey: &pk})
	if err != nil {
		return fmt.Errorf("search: add documents: %w", err)
	}
	_, err = c.wait(ctx, task, false)
	return err
}

// Delete removes products of the store in ctx. The store clause makes it
// a no-op for IDs of other stores.
func (c *Client) Delete(ctx context.Context, ids []ulid.ID) error {
	if len(ids) == 0 {
		return nil
	}
	f, _, err := storeFilter(ctx)
	if err != nil {
		return err
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = quote(id.String())
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	task, err := c.index().DeleteDocumentsByFilterWithContext(ctx, f+" AND id IN ["+strings.Join(quoted, ", ")+"]", nil)
	if err != nil {
		return fmt.Errorf("search: delete documents: %w", err)
	}
	_, err = c.wait(ctx, task, false)
	return err
}

// Search returns products of the store in ctx only.
func (c *Client) Search(ctx context.Context, q Query) (Result, error) {
	f, store, err := storeFilter(ctx)
	if err != nil {
		return Result{}, err
	}
	if q.Status != "" {
		f += " AND status = " + quote(q.Status)
	}
	if q.CategoryID != nil {
		f += " AND category_ids = " + quote(q.CategoryID.String())
	}
	req := &meilisearch.SearchRequest{Filter: f, Limit: int64(q.Limit), Offset: int64(q.Offset)}
	if q.Text == "" {
		req.Sort = []string{"created_at:desc"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	res, err := c.index().SearchWithContext(ctx, q.Text, req)
	if err != nil {
		return Result{}, fmt.Errorf("search: %w", err)
	}
	var docs []Doc
	if err = res.Hits.DecodeInto(&docs); err != nil {
		return Result{}, fmt.Errorf("search: decode hits: %w", err)
	}
	if docs == nil {
		docs = []Doc{}
	}
	// Second wall: a hit of another store means the filter is broken.
	for _, d := range docs {
		if d.StoreID != store.String() {
			return Result{}, fmt.Errorf("search: %w (hit %s)", ErrForeignDoc, d.ID)
		}
	}
	return Result{Hits: docs, EstimatedTotal: res.EstimatedTotalHits}, nil
}
