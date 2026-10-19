package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Image limits (ADR-0012).
const (
	MaxProductImages = 20
	MaxImageAltLen   = 200
	MaxImagePosition = 100
	// UploadURLTTL is how long a presigned upload stays valid.
	UploadURLTTL = 15 * time.Minute
	// PendingUploadTTL is how long an image may wait for its upload before
	// the row and any partial object are removed.
	PendingUploadTTL = time.Hour
)

// ImageStatus is an image's lifecycle state.
type ImageStatus string

// Image statuses.
const (
	ImagePending    ImageStatus = "pending"    // waiting for the browser's upload
	ImageProcessing ImageStatus = "processing" // uploaded; the job is validating and re-encoding it
	ImageReady      ImageStatus = "ready"      // a clean master exists and can be delivered
	ImageFailed     ImageStatus = "failed"     // rejected; FailureReason says why
)

// Image is a product image.
type Image struct {
	ID            ulid.ID
	ProductID     ulid.ID
	Status        ImageStatus
	UploadKey     string
	ObjectKey     string // the master, once ready
	Format        string // jpeg or png, once ready
	Width         int
	Height        int
	Bytes         int64
	Alt           string
	Position      int
	FailureReason string
	CreatedAt     time.Time
}

// UploadContentTypes are the types a merchant may upload. The declared
// type only shapes the upload policy; the job sniffs the real type.
func UploadContentTypes() []string {
	return []string{"image/jpeg", "image/png", "image/webp", "image/gif"}
}

// ImageInput is what the merchant sets on an image.
type ImageInput struct {
	Alt      string
	Position int
}

// ValidateImageInput normalizes and checks alt text and position.
func ValidateImageInput(in ImageInput) (ImageInput, error) {
	in.Alt = strings.TrimSpace(in.Alt)
	var fields []errs.FieldError
	if fe, ok := checkText("alt", in.Alt, MaxImageAltLen, true); !ok {
		fields = append(fields, fe)
	}
	if in.Position < 0 || in.Position > MaxImagePosition {
		fields = append(fields, errs.FieldError{Field: "position", Code: "range", Message: fmt.Sprintf("position must be between 0 and %d", MaxImagePosition)})
	}
	if len(fields) > 0 {
		return ImageInput{}, errs.Validation(fields...)
	}
	return in, nil
}

// ValidateContentType checks a declared upload type.
func ValidateContentType(ct string) (string, error) {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if !utf8.ValidString(ct) || !slices.Contains(UploadContentTypes(), ct) {
		return "", errs.Validation(errs.FieldError{Field: "content_type", Code: "unsupported",
			Message: "content_type must be image/jpeg, image/png, image/webp or image/gif"})
	}
	return ct, nil
}

// StorePrefix is the object-key prefix every object of a store lives
// under; workers refuse keys outside it.
func StorePrefix(store tenant.StoreID) string { return "stores/" + store.String() + "/" }

// UploadKey is where the browser uploads the original of an image.
func UploadKey(store tenant.StoreID, image ulid.ID) string {
	return StorePrefix(store) + "uploads/" + image.String()
}

// MasterKey is where the re-encoded master of an image lives.
func MasterKey(store tenant.StoreID, image ulid.ID, format string) string {
	ext := "jpg"
	if format == "png" {
		ext = "png"
	}
	return StorePrefix(store) + "images/" + image.String() + "." + ext
}
