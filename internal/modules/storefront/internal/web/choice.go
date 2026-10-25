package web

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// maxChoices is the most options a product has (the catalog allows
// three), and maxChoiceLen the longest option value a form may send.
const (
	maxChoices   = 3
	maxChoiceLen = 100
)

// choiceFromForm reads the product page's form when it names a product
// and one value per option (o0, o1, o2) instead of a variant: what the
// page sends without JavaScript. Values must be consecutive, single and
// short.
func choiceFromForm(form url.Values) (ulid.ID, []string, bool) {
	id, err := ulid.Parse(form.Get("product"))
	if err != nil {
		return ulid.ID{}, nil, false
	}
	var values []string
	gap := false
	for i := range maxChoices {
		vs, ok := form[fmt.Sprintf("o%d", i)]
		switch {
		case !ok:
			gap = true
			continue
		case gap, len(vs) != 1, len(vs[0]) > maxChoiceLen:
			return ulid.ID{}, nil, false
		}
		values = append(values, vs[0])
	}
	return id, values, true
}

// pickVariant is the variant whose option values are exactly values.
func pickVariant(pr catalog.StorefrontProduct, values []string) (ulid.ID, bool) {
	for _, v := range pr.Variants {
		if len(values) > 0 && slices.Equal(v.OptionValues, values) {
			return v.ID, true
		}
	}
	if len(values) == 0 && len(pr.Variants) == 1 && len(pr.Options) == 0 {
		return pr.Variants[0].ID, true // nothing to choose
	}
	return ulid.ID{}, false
}

// chosenVariant resolves the form's choice to a variant of an active
// product of this store in one catalog read; ok is false when the
// product or the combination does not exist.
func (s *Shop) chosenVariant(ctx context.Context, form url.Values) (ulid.ID, bool, error) {
	pid, values, ok := choiceFromForm(form)
	if !ok {
		return ulid.ID{}, false, nil
	}
	ps, err := s.catalog.ProductsByIDs(ctx, []ulid.ID{pid})
	if err != nil {
		return ulid.ID{}, false, fmt.Errorf("web: chosen product: %w", err)
	}
	if len(ps) != 1 {
		return ulid.ID{}, false, nil
	}
	id, ok := pickVariant(ps[0], values)
	return id, ok, nil
}
