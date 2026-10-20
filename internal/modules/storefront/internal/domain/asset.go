package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// AssetKind is what a store asset is for.
type AssetKind string

// Asset kinds. A store has at most one ready asset of each kind.
const (
	AssetLogo AssetKind = "logo"
)

// AssetStatus is an asset's lifecycle state, as for product images
// (ADR-0012).
type AssetStatus string

// Asset statuses.
const (
	AssetPending    AssetStatus = "pending"    // waiting for the browser's upload
	AssetProcessing AssetStatus = "processing" // uploaded; the job is re-encoding it
	AssetReady      AssetStatus = "ready"      // a clean master is live
	AssetFailed     AssetStatus = "failed"     // rejected; FailureReason says why
)

// Asset limits.
const (
	MaxLogoBytes = 2 << 20 // logos are small; a 2 MiB original is plenty
	// AssetUploadTTL is how long a presigned upload stays valid.
	AssetUploadTTL = 15 * time.Minute
	// PendingAssetTTL is how long an asset may wait for its upload before
	// it and any partial object are removed.
	PendingAssetTTL = time.Hour
)

// Asset is an image the store uses for its look.
type Asset struct {
	ID            ulid.ID
	Kind          AssetKind
	Status        AssetStatus
	UploadKey     string
	ObjectKey     string // the master, once ready
	Format        string // jpeg or png, once ready
	Width         int
	Height        int
	Bytes         int64
	FailureReason string
	CreatedAt     time.Time
}

// LogoContentTypes are the types a merchant may upload as a logo. GIF and
// SVG are refused: animation has no place in a header, and SVG can carry
// script.
func LogoContentTypes() []string { return []string{"image/png", "image/jpeg", "image/webp"} }

// ValidateLogoContentType normalizes and checks the declared type of a
// logo upload. The job sniffs the real type anyway.
func ValidateLogoContentType(ct string) (string, error) {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if !slices.Contains(LogoContentTypes(), ct) {
		return "", errs.Validation(errs.FieldError{Field: "content_type", Code: "unsupported", Message: "a logo must be PNG, JPEG or WebP"})
	}
	return ct, nil
}

// AssetUploadKey is where the browser uploads an asset's original.
func AssetUploadKey(store tenant.StoreID, id ulid.ID) string {
	return store.ObjectPrefix() + "uploads/" + id.String()
}

// AssetMasterKey is where an asset's re-encoded master lives.
func AssetMasterKey(store tenant.StoreID, id ulid.ID, format string) string {
	ext := "jpg"
	if format == "png" {
		ext = "png"
	}
	return store.ObjectPrefix() + "assets/" + id.String() + "." + ext
}
