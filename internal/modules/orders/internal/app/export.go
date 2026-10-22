package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Export limits.
const (
	MaxExportRows   = 100_000
	exportPageSize  = 500
	exportTimeStamp = "2006-01-02 15:04"
)

// exportHeader is the CSV's first row. Column names stay English (they
// are keys for spreadsheets and couriers' import tools).
func exportHeader() []string {
	return []string{"order", "created_at", "status", "name", "phone", "governorate", "city", "address", "notes",
		"items", "item_count", "subtotal", "shipping", "total", "currency"}
}

// Export writes the orders matching q (newest first, at most
// MaxExportRows) to w as CSV, page by page, each page in one tenant
// transaction with its lines in one query. Times are in the store's
// zone; amounts are plain decimals. A UTF-8 BOM leads so spreadsheet
// apps read the Arabic text. The export itself is audited (it carries
// customers' phones and addresses).
func (s *Service) Export(ctx context.Context, q domain.Query, w io.Writer) (rows int, err error) {
	info, err := s.stores(ctx)
	if err != nil {
		return 0, err
	}
	f, err := domain.ResolveQuery(q, info.Country)
	if err != nil {
		return 0, err
	}
	loc := info.Location
	if loc == nil {
		loc = time.UTC
	}
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "order.exported", TargetType: "order",
			Metadata: map[string]any{"status": statusOf(f), "from": f.From, "to": f.To}})
	}); err != nil {
		return 0, err
	}
	if _, err = io.WriteString(w, "\ufeff"); err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	if err = cw.Write(exportHeader()); err != nil {
		return 0, err
	}
	f.Limit = exportPageSize
	for rows < MaxExportRows {
		f.Limit = min(exportPageSize, MaxExportRows-rows)
		var page []domain.Order
		if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			var txErr error
			if page, txErr = store.List(ctx, tx, f); txErr != nil {
				return txErr
			}
			return store.LinesOf(ctx, tx, page)
		}); err != nil {
			return rows, err
		}
		for _, o := range page {
			if err = cw.Write(exportRow(o, info.Country, loc)); err != nil {
				return rows, err
			}
		}
		rows += len(page)
		cw.Flush()
		if err = cw.Error(); err != nil {
			return rows, err
		}
		if len(page) < f.Limit {
			break
		}
		f.Before = page[len(page)-1].ID
		if t := domain.CreatedAtOf(f.Before).Add(time.Millisecond); t.Before(f.To) {
			f.To = t
		}
	}
	return rows, nil
}

func statusOf(f domain.Filter) string {
	if f.Status == nil {
		return ""
	}
	return string(*f.Status)
}

func exportRow(o domain.Order, country geo.Country, loc *time.Location) []string {
	c := o.Contact
	gov := c.Governorate
	if a, ok := geo.Lookup(country, gov); ok {
		gov = a.Name("ar")
	}
	items := make([]string, len(o.Lines))
	for i, l := range o.Lines {
		t := l.Title
		if l.VariantTitle != "" {
			t += " (" + l.VariantTitle + ")"
		}
		items[i] = fmt.Sprintf("%d× %s", l.Qty, t)
	}
	row := []string{o.Code(), o.CreatedAt.In(loc).Format(exportTimeStamp), string(o.Status), c.Name, c.Phone.Unmasked(), gov,
		c.City, c.Address, c.Notes, strings.Join(items, "; "), strconv.Itoa(o.ItemCount),
		decimal(o.Subtotal), decimal(o.Shipping), decimal(o.Total), string(o.Total.Currency())}
	for i := range row {
		row[i] = cell(row[i])
	}
	return row
}

// cell neutralizes spreadsheet formulas (CSV injection): text that a
// spreadsheet would evaluate gets a leading apostrophe. Shoppers type
// names, addresses and notes, so every text cell is untrusted.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// decimal writes an amount as a plain decimal ("1234.50"): no grouping,
// no symbol, so spreadsheets read it as a number.
func decimal(m money.Money) string {
	units := m.Currency().MinorUnits()
	a := m.Amount()
	neg := a < 0
	if neg {
		a = -a
	}
	s := strconv.FormatInt(a, 10)
	if units > 0 {
		if len(s) <= units {
			s = strings.Repeat("0", units-len(s)+1) + s
		}
		s = s[:len(s)-units] + "." + s[len(s)-units:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
