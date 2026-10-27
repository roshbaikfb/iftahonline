package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func sizeChartErr(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return fmt.Errorf("catalog store: %s: %w", op, err)
}

// SaveSizeChart creates (isNew) or replaces a size chart.
func SaveSizeChart(ctx context.Context, tx Tx, id ulid.ID, in domain.SizeChartInput, isNew bool) (domain.SizeChart, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.SizeChart{}, err
	}
	header, err := json.Marshal(in.Header)
	if err != nil {
		return domain.SizeChart{}, fmt.Errorf("catalog store: encode size chart header: %w", err)
	}
	body, err := json.Marshal(in.Body)
	if err != nil {
		return domain.SizeChart{}, fmt.Errorf("catalog store: encode size chart body: %w", err)
	}
	q := catalogdb.New(tx)
	var row catalogdb.SizeChart
	if isNew {
		row, err = q.InsertSizeChart(ctx, catalogdb.InsertSizeChartParams{ID: id, StoreID: store, Name: in.Name, Header: header, Body: body})
	} else {
		row, err = q.UpdateSizeChart(ctx, catalogdb.UpdateSizeChartParams{ID: id, StoreID: store, Name: in.Name, Header: header, Body: body})
	}
	if err != nil {
		return domain.SizeChart{}, sizeChartErr(err, "save size chart")
	}
	return toSizeChart(row)
}

// GetSizeChart returns one size chart with how many live products use it.
func GetSizeChart(ctx context.Context, tx Tx, id ulid.ID) (domain.SizeChart, error) {
	c, err := SizeChartOnly(ctx, tx, id)
	if err != nil {
		return domain.SizeChart{}, err
	}
	store, err := current(ctx)
	if err != nil {
		return domain.SizeChart{}, err
	}
	n, err := catalogdb.New(tx).CountSizeChartProducts(ctx, catalogdb.CountSizeChartProductsParams{StoreID: store, ID: &id})
	if err != nil {
		return domain.SizeChart{}, sizeChartErr(err, "count size chart products")
	}
	c.Products = int(n)
	return c, nil
}

// SizeChartOnly returns one size chart without its product count (the
// product page).
func SizeChartOnly(ctx context.Context, tx Tx, id ulid.ID) (domain.SizeChart, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.SizeChart{}, err
	}
	row, err := catalogdb.New(tx).GetSizeChart(ctx, catalogdb.GetSizeChartParams{StoreID: store, ID: id})
	if err != nil {
		return domain.SizeChart{}, sizeChartErr(err, "get size chart")
	}
	return toSizeChart(row)
}

// DeleteSizeChart deletes a size chart; its products lose it.
func DeleteSizeChart(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	n, err := catalogdb.New(tx).DeleteSizeChart(ctx, catalogdb.DeleteSizeChartParams{StoreID: store, ID: id})
	if err != nil {
		return sizeChartErr(err, "delete size chart")
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListSizeCharts returns every size chart of the store (at most
// domain.MaxSizeChartsPerStore), by name, with product counts.
func ListSizeCharts(ctx context.Context, tx Tx) ([]domain.SizeChart, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).ListSizeCharts(ctx, catalogdb.ListSizeChartsParams{StoreID: store, Lim: domain.MaxSizeChartsPerStore})
	if err != nil {
		return nil, sizeChartErr(err, "list size charts")
	}
	out := make([]domain.SizeChart, len(rows))
	for i, r := range rows {
		c, err := toSizeChart(catalogdb.SizeChart{ID: r.ID, StoreID: r.StoreID, Name: r.Name, Header: r.Header, Body: r.Body,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
		if err != nil {
			return nil, err
		}
		c.Products = int(r.Products)
		out[i] = c
	}
	return out, nil
}

// CountSizeCharts counts the store's size charts.
func CountSizeCharts(ctx context.Context, tx Tx) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := catalogdb.New(tx).CountSizeCharts(ctx, store)
	if err != nil {
		return 0, sizeChartErr(err, "count size charts")
	}
	return int(n), nil
}

func toSizeChart(r catalogdb.SizeChart) (domain.SizeChart, error) {
	c := domain.SizeChart{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if err := json.Unmarshal(r.Header, &c.Header); err != nil {
		return domain.SizeChart{}, fmt.Errorf("catalog store: decode header of size chart %s: %w", r.ID, err)
	}
	if err := json.Unmarshal(r.Body, &c.Body); err != nil {
		return domain.SizeChart{}, fmt.Errorf("catalog store: decode body of size chart %s: %w", r.ID, err)
	}
	return c, nil
}
