package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store/storefrontdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CartByToken returns the live cart whose token hashes to tokenHash, or
// domain.ErrNotFound.
func CartByToken(ctx context.Context, tx Tx, tokenHash []byte) (ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return ulid.ID{}, err
	}
	id, err := storefrontdb.New(tx).GetCartByToken(ctx, storefrontdb.GetCartByTokenParams{StoreID: store, TokenHash: tokenHash})
	if err != nil {
		return ulid.ID{}, notFound(err, "get cart")
	}
	return id, nil
}

// InsertCart creates an empty cart.
func InsertCart(ctx context.Context, tx Tx, id ulid.ID, tokenHash []byte, expires time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	err = storefrontdb.New(tx).InsertCart(ctx, storefrontdb.InsertCartParams{ID: id, StoreID: store, TokenHash: tokenHash, ExpiresAt: expires})
	if err != nil {
		return fmt.Errorf("storefront store: insert cart: %w", err)
	}
	return nil
}

// TouchCart renews a cart's expiry.
func TouchCart(ctx context.Context, tx Tx, id ulid.ID, expires time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = storefrontdb.New(tx).TouchCart(ctx, storefrontdb.TouchCartParams{StoreID: store, ID: id, ExpiresAt: expires}); err != nil {
		return fmt.Errorf("storefront store: touch cart: %w", err)
	}
	return nil
}

// CartLines returns a cart's lines, oldest first.
func CartLines(ctx context.Context, tx Tx, cartID ulid.ID) ([]domain.CartLine, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := storefrontdb.New(tx).ListCartItems(ctx, storefrontdb.ListCartItemsParams{StoreID: store, CartID: cartID})
	if err != nil {
		return nil, fmt.Errorf("storefront store: list cart items: %w", err)
	}
	out := make([]domain.CartLine, len(rows))
	for i, r := range rows {
		out[i] = domain.CartLine{VariantID: r.VariantID, Qty: int(r.Qty)}
	}
	return out, nil
}

// CountCartLines counts a cart's lines.
func CountCartLines(ctx context.Context, tx Tx, cartID ulid.ID) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := storefrontdb.New(tx).CountCartLines(ctx, storefrontdb.CountCartLinesParams{StoreID: store, CartID: cartID})
	if err != nil {
		return 0, fmt.Errorf("storefront store: count cart lines: %w", err)
	}
	return int(n), nil
}

// AddCartItem adds qty of a variant (to what is there, capped) and
// returns the line's new quantity.
func AddCartItem(ctx context.Context, tx Tx, cartID, variantID ulid.ID, qty int) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := storefrontdb.New(tx).AddCartItem(ctx, storefrontdb.AddCartItemParams{
		StoreID: store, CartID: cartID, VariantID: variantID, Qty: int32(qty), //nolint:gosec // bounded by domain.MaxLineQty
	})
	if err != nil {
		return 0, fmt.Errorf("storefront store: add cart item: %w", err)
	}
	return int(n), nil
}

// SetCartItem sets a line's quantity, or deletes it at zero. A line not
// in the cart is domain.ErrNotFound.
func SetCartItem(ctx context.Context, tx Tx, cartID, variantID ulid.ID, qty int) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := storefrontdb.New(tx)
	if qty == 0 {
		if err = q.DeleteCartItem(ctx, storefrontdb.DeleteCartItemParams{StoreID: store, CartID: cartID, VariantID: variantID}); err != nil {
			return fmt.Errorf("storefront store: delete cart item: %w", err)
		}
		return nil
	}
	n, err := q.SetCartItem(ctx, storefrontdb.SetCartItemParams{
		StoreID: store, CartID: cartID, VariantID: variantID, Qty: int32(qty), //nolint:gosec // bounded by domain.MaxLineQty
	})
	if err != nil {
		return fmt.Errorf("storefront store: set cart item: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CartQuantity is the number of units in the cart a token opens (0 when
// there is none).
func CartQuantity(ctx context.Context, tx Tx, tokenHash []byte) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := storefrontdb.New(tx).CartQuantity(ctx, storefrontdb.CartQuantityParams{StoreID: store, TokenHash: tokenHash})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("storefront store: cart quantity: %w", err)
	}
	return int(n), nil
}
