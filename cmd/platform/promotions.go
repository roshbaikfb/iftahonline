package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// newPromotions builds the coupons module (every role: the api serves
// the merchant's coupons, orders apply them on every role that places
// orders, the worker gives back cancelled orders' uses).
func newPromotions(c clients) (*promotions.Module, error) {
	return promotions.New(promotions.Deps{Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids)})
}

// orderCoupons adapts promotions to the orders module's coupon port
// (ADR-0042): orders never import promotions.
type orderCoupons struct{ c promotions.Coupons }

var _ orders.Coupons = orderCoupons{}

func (o orderCoupons) Preview(ctx context.Context, code string, subtotal money.Money) (orders.CouponDiscount, error) {
	return toOrderDiscount(o.c.Preview(ctx, code, subtotal))
}

func (o orderCoupons) RedeemTx(ctx context.Context, tx pgx.Tx, code string, subtotal money.Money, customer, order ulid.ID) (orders.CouponDiscount, error) {
	return toOrderDiscount(o.c.RedeemTx(ctx, tx, code, subtotal, customer, order))
}

func toOrderDiscount(d promotions.Discount, err error) (orders.CouponDiscount, error) {
	var r *promotions.Rejected
	if errors.As(err, &r) {
		return orders.CouponDiscount{}, &orders.CouponRejected{Reason: string(r.Reason), MinSubtotalMinor: r.MinSubtotalMinor}
	}
	if err != nil {
		return orders.CouponDiscount{}, err
	}
	return orders.CouponDiscount{Amount: d.Amount, FreeShipping: d.FreeShipping}, nil
}
