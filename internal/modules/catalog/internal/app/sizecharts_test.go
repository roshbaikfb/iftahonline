package app_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func chart(name string) domain.SizeChartInput {
	return domain.SizeChartInput{Name: name, Header: []string{"المقاس", "الصدر", "الطول"},
		Body: [][]string{{"S", "96", "68"}, {"M", "102", "71"}}}
}

func sizeChartEvent(c domain.SizeChart, reason string) string {
	return fmt.Sprintf("%s %s %s", domain.EventSizeChartChanged, c.ID, reason)
}

func TestSizeChartLifecycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	c, err := f.svc.CreateSizeChart(ctx, chart(" تيشيرتات "))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "تيشيرتات" || len(c.Body) != 2 || c.Products != 0 {
		t.Fatalf("created %+v", c)
	}
	wantEvents(t, f) // nothing shows a new chart yet
	if !f.audit.has("size_chart.created") {
		t.Fatal("create not audited")
	}

	// a product takes it; the list and the chart count it
	in := tee("Tee", 3)
	in.SizeChartID = &c.ID
	p := mustCreate(ctx, t, f, in)
	if p.SizeChartID == nil || *p.SizeChartID != c.ID {
		t.Fatalf("product chart = %v", p.SizeChartID)
	}
	f.events.take()
	if _, err = f.svc.CreateSizeChart(ctx, chart("بناطيل")); err != nil {
		t.Fatal(err)
	}
	list, err := f.svc.ListSizeCharts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "بناطيل" || list[1].Products != 1 || list[0].Products != 0 {
		t.Fatalf("list = %+v", list)
	}

	// replacing it changes every product page using it
	next := chart("تيشيرتات رجالي")
	next.Body = append(next.Body, []string{"L", "108", "73"})
	got, err := f.svc.UpdateSizeChart(ctx, c.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "تيشيرتات رجالي" || len(got.Body) != 3 || got.Products != 1 {
		t.Fatalf("updated %+v", got)
	}
	wantEvents(t, f, sizeChartEvent(c, domain.SizeChartUpdated))
	if got, err = f.svc.GetSizeChart(ctx, c.ID); err != nil || got.Products != 1 {
		t.Fatalf("get = %+v, %v", got, err)
	}

	// deleting it leaves its products without a chart
	if err = f.svc.DeleteSizeChart(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, sizeChartEvent(c, domain.SizeChartDeleted))
	if p, err = f.svc.GetProduct(ctx, p.ID); err != nil || p.SizeChartID != nil {
		t.Fatalf("product after delete: chart %v, %v", p.SizeChartID, err)
	}
	if !f.audit.has("size_chart.updated") || !f.audit.has("size_chart.deleted") {
		t.Fatal("update or delete not audited")
	}
	for _, err := range []error{
		func() error { _, e := f.svc.GetSizeChart(ctx, c.ID); return e }(),
		func() error { _, e := f.svc.UpdateSizeChart(ctx, c.ID, chart("x")); return e }(),
		f.svc.DeleteSizeChart(ctx, c.ID),
	} {
		wantKind(t, err, errs.KindNotFound)
	}
	_, err = f.svc.CreateSizeChart(ctx, domain.SizeChartInput{Name: "x"})
	wantField(t, err, "header", "count")
}

func TestProductSizeChartMustBeTheStores(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other, err := f.svc.CreateSizeChart(f.ctxB, chart("B's"))
	if err != nil {
		t.Fatal(err)
	}
	in := tee("Tee", 1)
	in.SizeChartID = &other.ID
	_, err = f.svc.CreateProduct(f.ctxA, in)
	wantField(t, err, "size_chart_id", "unknown")

	p := mustCreate(f.ctxA, t, f, tee("Tee", 1))
	upd := tee("Tee", 1)
	upd.SizeChartID = &other.ID
	_, err = f.svc.UpdateProduct(f.ctxA, p.ID, upd)
	wantField(t, err, "size_chart_id", "unknown")

	// store A sees none of B's charts
	list, err := f.svc.ListSizeCharts(f.ctxA)
	if err != nil || len(list) != 0 {
		t.Fatalf("A's list = %+v, %v", list, err)
	}
	for _, err := range []error{
		func() error { _, err := f.svc.GetSizeChart(f.ctxA, other.ID); return err }(),
		func() error { _, err := f.svc.UpdateSizeChart(f.ctxA, other.ID, chart("x")); return err }(),
		f.svc.DeleteSizeChart(f.ctxA, other.ID),
	} {
		wantKind(t, err, errs.KindNotFound)
	}
	if got, err := f.svc.GetSizeChart(f.ctxB, other.ID); err != nil || got.Name != "B's" {
		t.Fatalf("B's chart changed: %+v, %v", got, err)
	}
}

func TestSizeChartLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for i := range domain.MaxSizeChartsPerStore {
		if _, err := f.svc.CreateSizeChart(f.ctxA, chart(fmt.Sprintf("chart %03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.svc.CreateSizeChart(f.ctxA, chart("one more"))
	wantKind(t, err, errs.KindConflict)
	if _, err = f.svc.CreateSizeChart(f.ctxB, chart("B")); err != nil {
		t.Fatalf("another store's limit is its own: %v", err)
	}
	// the list is one query whatever the number of charts
	n := countQueries(t, f, func() error {
		list, err := f.svc.ListSizeCharts(f.ctxA)
		if err == nil && len(list) != domain.MaxSizeChartsPerStore {
			err = fmt.Errorf("listed %d", len(list))
		}
		return err
	})
	if n > 4 { // begin, set store, the list, commit
		t.Fatalf("list made %d queries", n)
	}
}

func TestProductColorsRoundTrip(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	in := tee("Tee", 1)
	in.Options = append(in.Options, domain.Option{Name: "اللون", Values: []string{"كحلي"}, Colors: []string{"#1F2A44"}})
	for i := range in.Variants {
		in.Variants[i].OptionValues = append(in.Variants[i].OptionValues, "كحلي")
	}
	p := mustCreate(f.ctxA, t, f, in)
	if !slices.Equal(p.Options[1].Colors, []string{"#1f2a44"}) || p.Options[0].Colors != nil {
		t.Fatalf("options = %+v", p.Options)
	}
}

func TestStorefrontShowsTheSizeChart(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := newStorefront(f)
	c, err := f.svc.CreateSizeChart(f.ctxA, chart("تيشيرتات"))
	if err != nil {
		t.Fatal(err)
	}
	in := tee("Tee", 1)
	in.SizeChartID = &c.ID
	p := mustCreate(f.ctxA, t, f, in)
	plain := mustCreate(f.ctxA, t, f, tee("Plain", 1))

	got, err := sf.ProductBySlug(f.ctxA, p.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.SizeChart == nil || !slices.Equal(got.SizeChart.Header, c.Header) || len(got.SizeChart.Body) != 2 {
		t.Fatalf("size chart = %+v", got.SizeChart)
	}
	// one more query for the chart, none without one
	with := countQueries(t, f, func() error { _, e := sf.ProductBySlug(f.ctxA, p.Slug); return e })
	without := countQueries(t, f, func() error { _, e := sf.ProductBySlug(f.ctxA, plain.Slug); return e })
	if with != without+1 {
		t.Fatalf("queries with a chart %d, without %d", with, without)
	}
	if got, err = sf.ProductBySlug(f.ctxA, plain.Slug); err != nil || got.SizeChart != nil {
		t.Fatalf("plain product chart = %+v, %v", got.SizeChart, err)
	}
	// lists carry no chart
	page, err := sf.ListProducts(f.ctxA, app.StorefrontQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.SizeChart != nil {
			t.Fatalf("listed %s with a chart", it.Slug)
		}
	}
}
