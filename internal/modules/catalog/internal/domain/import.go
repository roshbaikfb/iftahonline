package domain

import (
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Import limits (ADR-0014).
const (
	MaxImportFileBytes = 10 << 20 // CSV
	MaxImportRecords   = 10_000   // products per import
	MaxImportRowErrors = 100      // kept for the merchant; the count goes on
)

// ImportKind is where products come from.
type ImportKind string

// Import kinds.
const (
	ImportCSV         ImportKind = "csv"
	ImportWooCommerce ImportKind = "woocommerce"
)

// ParseImportKind validates a kind.
func ParseImportKind(s string) (ImportKind, bool) {
	k := ImportKind(s)
	return k, k == ImportCSV || k == ImportWooCommerce
}

// UploadContentType is the content type a file import is uploaded as.
func (ImportKind) UploadContentType() string { return "text/csv" }

// ImportStatus is an import's lifecycle state.
type ImportStatus string

// Import statuses.
const (
	ImportPending   ImportStatus = "pending"   // waiting for the file upload (or the start call)
	ImportQueued    ImportStatus = "queued"    // the job is enqueued
	ImportRunning   ImportStatus = "running"   // records are being imported
	ImportSucceeded ImportStatus = "succeeded" // done; row errors may still exist
	ImportFailed    ImportStatus = "failed"    // the source could not be read; FailureReason says why
)

// RowError is one record that could not be imported.
type RowError struct {
	Line    int    `json:"line"` // CSV line, or the WooCommerce position (1-based)
	Message string `json:"message"`
}

// Import is one run of an importer.
type Import struct {
	ID            ulid.ID
	Kind          ImportKind
	Status        ImportStatus
	SourceKey     string // the uploaded file
	WooURL        string
	Total         int
	Cursor        int // records handled so far
	Created       int
	Updated       int
	Failed        int
	RowErrors     []RowError
	FailureReason string
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

// ImportRecord is one product read from a source. A record that could
// not be read carries Err (shown to the merchant) instead of a product.
type ImportRecord struct {
	Line       int
	Product    ProductInput
	Categories []string // category names, created when missing
	Err        string
}

// ImportKey is where a file import is uploaded.
func ImportKey(store tenant.StoreID, id ulid.ID) string {
	return StorePrefix(store) + "imports/" + id.String()
}
