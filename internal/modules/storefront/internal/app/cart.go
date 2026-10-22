package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Variants is the catalog's view of what can be sold (catalog.Catalog).
type Variants interface {
	SellableVariants(ctx context.Context, ids []ulid.ID) ([]catalog.SellableVariant, error)
}

// Cart errors. They are shown to shoppers, so they say what to do.
var (
	ErrVariantUnavailable = errs.Conflict("variant_unavailable", "this item is no longer available")
	ErrInsufficientStock  = errs.Conflict("insufficient_stock", "not enough of this item is left for that quantity")
	ErrCartFull           = errs.Conflict("cart_full", fmt.Sprintf("a cart holds at most %d different items", domain.MaxCartLines))
)

// CartLineView is a cart line with the variant's current price and stock.
type CartLineView struct {
	catalog.SellableVariant
	Qty       int
	Total     money.Money
	Available bool // the quantity can still be bought
}

// CartView is a shopper's cart as it stands now.
type CartView struct {
	Lines    []CartLineView // sellable lines only; others drop out
	Subtotal *money.Money   // nil for an empty cart
	Units    int
}

// CartStore keeps carts (store.Carts, in Redis; ADR-0022). Carts are
// found by the hash of the shopper's token, in the store in ctx.
type CartStore interface {
	Exists(ctx context.Context, tokenHash []byte) (bool, error)
	Lines(ctx context.Context, tokenHash []byte) ([]domain.CartLine, error)
	Qty(ctx context.Context, tokenHash []byte, v ulid.ID) (int, error)
	Put(ctx context.Context, tokenHash []byte, v ulid.ID, qty int, existingOnly bool) error
	Remove(ctx context.Context, tokenHash []byte, v ulid.ID) error
	Units(ctx context.Context, tokenHash []byte) (int, error)
	Delete(ctx context.Context, tokenHash []byte) error
}

// AddToCart adds qty of a variant to the cart that token opens, creating
// a cart (and a new token) when there is none. The variant must be for
// sale, and the line's new quantity (capped at domain.MaxLineQty) in
// stock. It returns the token to keep in the shopper's cookie.
func (s *Service) AddToCart(ctx context.Context, token authtoken.Token, variantID ulid.ID, qty int) (authtoken.Token, error) {
	if err := domain.ValidateQty(qty, false); err != nil {
		return "", err
	}
	v, err := s.sellable(ctx, variantID)
	if err != nil {
		return "", err
	}
	live := false
	if token != "" {
		if live, err = s.carts.Exists(ctx, token.Hash()); err != nil {
			return "", err
		}
	}
	if !live {
		if token, err = authtoken.New(); err != nil {
			return "", err
		}
	}
	cur, err := s.carts.Qty(ctx, token.Hash(), variantID)
	if err != nil {
		return "", err
	}
	n := min(cur+qty, domain.MaxLineQty)
	if !v.Stock.CanSell(n) {
		return "", ErrInsufficientStock
	}
	if err = s.carts.Put(ctx, token.Hash(), variantID, n, false); err != nil {
		if errors.Is(err, store.ErrCartFull) {
			return "", ErrCartFull
		}
		return "", err
	}
	return token, nil
}

// UpdateCartLine sets a line's quantity; zero removes it. Without a cart
// (or a line) there is nothing to update, which is not an error: the
// shopper's cart may simply have expired.
func (s *Service) UpdateCartLine(ctx context.Context, token authtoken.Token, variantID ulid.ID, qty int) error {
	if err := domain.ValidateQty(qty, true); err != nil {
		return err
	}
	if token == "" {
		return nil
	}
	if qty == 0 {
		return s.carts.Remove(ctx, token.Hash(), variantID)
	}
	v, err := s.sellable(ctx, variantID)
	if err != nil {
		return err
	}
	if !v.Stock.CanSell(qty) {
		return ErrInsufficientStock
	}
	if err = s.carts.Put(ctx, token.Hash(), variantID, qty, true); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

// Cart returns the cart that token opens with current prices, in two
// steps whatever its size: its lines, then the catalog for all of them.
// Lines the catalog no longer sells are left out.
func (s *Service) Cart(ctx context.Context, token authtoken.Token) (CartView, error) {
	if token == "" {
		return CartView{Lines: []CartLineView{}}, nil
	}
	lines, err := s.carts.Lines(ctx, token.Hash())
	if err != nil {
		return CartView{}, err
	}
	if len(lines) == 0 {
		return CartView{Lines: []CartLineView{}}, nil
	}
	ids := make([]ulid.ID, len(lines))
	for i, l := range lines {
		ids[i] = l.VariantID
	}
	vs, err := s.variants.SellableVariants(ctx, ids)
	if err != nil {
		return CartView{}, err
	}
	byID := make(map[ulid.ID]catalog.SellableVariant, len(vs))
	for _, v := range vs {
		byID[v.VariantID] = v
	}
	out := CartView{Lines: make([]CartLineView, 0, len(lines))}
	for _, l := range lines {
		v, ok := byID[l.VariantID]
		if !ok {
			continue
		}
		total, err := v.Price.MulInt(int64(l.Qty))
		if err != nil {
			return CartView{}, err
		}
		out.Lines = append(out.Lines, CartLineView{SellableVariant: v, Qty: l.Qty, Total: total, Available: v.Stock.CanSell(l.Qty)})
		out.Units += l.Qty
		if out.Subtotal == nil {
			out.Subtotal = &total
			continue
		}
		sum, err := out.Subtotal.Add(total)
		if err != nil {
			return CartView{}, err
		}
		out.Subtotal = &sum
	}
	return out, nil
}

// CartUnits is how many units the cart holds: the header's badge.
func (s *Service) CartUnits(ctx context.Context, token authtoken.Token) (int, error) {
	if token == "" {
		return 0, nil
	}
	return s.carts.Units(ctx, token.Hash())
}

// ClearCart empties the cart once its order is placed.
func (s *Service) ClearCart(ctx context.Context, token authtoken.Token) error {
	if token == "" {
		return nil
	}
	return s.carts.Delete(ctx, token.Hash())
}

func (s *Service) sellable(ctx context.Context, id ulid.ID) (catalog.SellableVariant, error) {
	vs, err := s.variants.SellableVariants(ctx, []ulid.ID{id})
	if err != nil {
		return catalog.SellableVariant{}, err
	}
	if len(vs) == 0 {
		return catalog.SellableVariant{}, ErrVariantUnavailable
	}
	return vs[0], nil
}
