package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
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

// AddToCart adds qty of a variant to the cart that token opens, creating
// a cart (and a new token) when there is none. The variant must be for
// sale, and the line's new quantity in stock. It returns the token to
// keep in the shopper's cookie.
func (s *Service) AddToCart(ctx context.Context, token authtoken.Token, variantID ulid.ID, qty int) (authtoken.Token, error) {
	if err := domain.ValidateQty(qty, false); err != nil {
		return "", err
	}
	v, err := s.sellable(ctx, variantID)
	if err != nil {
		return "", err
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cartID, txErr := s.openCart(ctx, tx, &token, true)
		if txErr != nil {
			return txErr
		}
		n, txErr := store.AddCartItem(ctx, tx, cartID, variantID, qty)
		if txErr != nil {
			return txErr
		}
		if !v.Stock.CanSell(n) {
			return ErrInsufficientStock
		}
		lines, txErr := store.CountCartLines(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		if lines > domain.MaxCartLines {
			return ErrCartFull
		}
		return store.TouchCart(ctx, tx, cartID, time.Now().Add(domain.CartTTL))
	})
	if err != nil {
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
	if qty > 0 {
		v, err := s.sellable(ctx, variantID)
		if err != nil {
			return err
		}
		if !v.Stock.CanSell(qty) {
			return ErrInsufficientStock
		}
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cartID, err := s.openCart(ctx, tx, &token, false)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = store.SetCartItem(ctx, tx, cartID, variantID, qty); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return store.TouchCart(ctx, tx, cartID, time.Now().Add(domain.CartTTL))
	})
}

// Cart returns the cart that token opens with current prices, in two
// steps whatever its size: its lines, then the catalog for all of them.
// Lines the catalog no longer sells are left out.
func (s *Service) Cart(ctx context.Context, token authtoken.Token) (CartView, error) {
	var lines []domain.CartLine
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cartID, err := s.openCart(ctx, tx, &token, false)
		if err != nil {
			return err
		}
		lines, err = store.CartLines(ctx, tx, cartID)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) || len(lines) == 0 {
		return CartView{Lines: []CartLineView{}}, nil
	}
	if err != nil {
		return CartView{}, err
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
	var n int
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		n, txErr = store.CartQuantity(ctx, tx, token.Hash())
		return txErr
	})
	return n, err
}

// openCart finds the cart token opens. With create, a missing cart is
// made, and token is replaced by the new cart's token.
func (s *Service) openCart(ctx context.Context, tx pgx.Tx, token *authtoken.Token, create bool) (ulid.ID, error) {
	if *token != "" {
		id, err := store.CartByToken(ctx, tx, token.Hash())
		if err == nil || !errors.Is(err, domain.ErrNotFound) || !create {
			return id, err
		}
	} else if !create {
		return ulid.ID{}, domain.ErrNotFound
	}
	fresh, err := authtoken.New()
	if err != nil {
		return ulid.ID{}, err
	}
	id := s.ids.New()
	if err = store.InsertCart(ctx, tx, id, fresh.Hash(), time.Now().Add(domain.CartTTL)); err != nil {
		return ulid.ID{}, err
	}
	*token = fresh
	return id, nil
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
