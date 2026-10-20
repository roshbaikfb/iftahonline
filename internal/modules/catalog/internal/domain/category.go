package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/slug"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Category limits.
const (
	// MaxCategoryDepth is the number of levels in the tree (root = 1).
	MaxCategoryDepth   = 3
	MaxCategoryNameLen = 120
	MaxCategoryPos     = 10_000
)

// Category is a node in the store's category tree.
type Category struct {
	ID        ulid.ID
	ParentID  *ulid.ID
	Slug      string
	Name      string
	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CategoryInput creates or replaces a category.
type CategoryInput struct {
	ParentID *ulid.ID
	Slug     string
	Name     string
	Position int
}

// ValidateCategory normalizes and checks a category input.
func ValidateCategory(in CategoryInput) (CategoryInput, error) {
	out := CategoryInput{ParentID: in.ParentID, Name: strings.TrimSpace(in.Name), Position: in.Position}
	out.Slug = slug.Normalize(in.Slug, out.Name)
	var fields []errs.FieldError
	if fe, ok := checkText("name", out.Name, MaxCategoryNameLen, false); !ok {
		fields = append(fields, fe)
	}
	if fe, ok := checkSlugOrDerived(out.Slug); !ok {
		fields = append(fields, fe)
	}
	if out.Position < 0 || out.Position > MaxCategoryPos {
		fields = append(fields, errs.FieldError{Field: "position", Code: "range", Message: fmt.Sprintf("position must be between 0 and %d", MaxCategoryPos)})
	}
	if len(fields) > 0 {
		return CategoryInput{}, errs.Validation(fields...)
	}
	return out, nil
}

// Tree errors.
var (
	ErrCategoryTooDeep = errs.Validation(errs.FieldError{Field: "parent_id", Code: "too_deep",
		Message: fmt.Sprintf("categories can be nested at most %d levels", MaxCategoryDepth)})
	ErrCategoryCycle = errs.Validation(errs.FieldError{Field: "parent_id", Code: "cycle",
		Message: "a category cannot be moved under itself or its descendants"})
)

// CheckPlacement validates putting a category (id, zero for a new one)
// whose subtree has subtreeHeight levels (1 for a leaf or a new category)
// under the parent whose ancestor chain, nearest first and the parent
// itself included, is parentChain.
func CheckPlacement(id ulid.ID, subtreeHeight int, parentChain []ulid.ID) error {
	for _, a := range parentChain {
		if !id.IsZero() && a == id {
			return ErrCategoryCycle
		}
	}
	if len(parentChain)+subtreeHeight > MaxCategoryDepth {
		return ErrCategoryTooDeep
	}
	return nil
}
