package app_test

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func open(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// TestContentEndToEnd: the site starts with the FAQ it shipped with; the
// console edits the contact details and the FAQ, and the site sees the
// change after the cache's minute; only the console's pool writes; a
// failed read keeps the last content. One test: the content is the
// platform's, one set for the package's database.
func TestContentEndToEnd(t *testing.T) {
	ctx := t.Context()
	appPool, adminPool := open(t, infra.AppURL), open(t, infra.AdminURL)
	clk := &clock{now: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}
	console := app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ulid.NewGenerator(), Now: clk.Now})
	site := app.NewService(app.Deps{DB: appPool, Now: clk.Now})

	// The migration's content: the shipped FAQ (five on the home page) and
	// the owner's number.
	c, err := site.Content(ctx)
	if err != nil {
		t.Fatal(err)
	}
	home := 0
	for _, f := range c.FAQs {
		if f.OnHome {
			home++
		}
		if f.QuestionEn == "" {
			t.Errorf("%q has no English", f.QuestionAr)
		}
	}
	if len(c.FAQs) != 10 || home != 5 || c.Settings.WhatsApp != "+201153450716" || c.FAQs[0].QuestionAr != "ما هي منصة افتح أونلاين؟" {
		t.Fatalf("seeded content = %d questions, %d on home, %+v", len(c.FAQs), home, c.Settings)
	}

	// Only the console writes.
	if _, err = site.SaveSettings(ctx, domain.Settings{}); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("site pool saves: %v", err)
	}
	if _, err = appPool.Exec(ctx, "UPDATE site_settings SET email = 'x@y.z'"); err == nil {
		t.Fatal("app_user wrote site_settings")
	}
	if _, err = appPool.Exec(ctx, "DELETE FROM site_faqs"); err == nil {
		t.Fatal("app_user deleted the FAQ")
	}

	// Settings: checked, trimmed, saved.
	if _, err = console.SaveSettings(ctx, domain.Settings{Email: "not an email"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a bad email: %v", err)
	}
	set, err := console.SaveSettings(ctx, domain.Settings{WhatsApp: "+201153450716", WhatsAppIntl: " +447700900123 ", Email: "hello@iftahonline.com",
		CompanyNumber: "12345678", Socials: map[domain.Social]string{domain.Instagram: "https://instagram.com/iftahonline", domain.X: " "}})
	if err != nil || set.WhatsAppIntl != "+447700900123" || len(set.Socials) != 1 {
		t.Fatalf("saved = %+v, %v", set, err)
	}
	if got, getErr := console.Settings(ctx); getErr != nil || got.Email != "hello@iftahonline.com" || got.Socials[domain.Instagram] == "" {
		t.Fatalf("settings = %+v, %v", got, getErr)
	}

	// FAQ: add, edit, delete.
	added, err := console.CreateFAQ(ctx, domain.FAQ{Position: 5, QuestionAr: "  سؤال جديد؟ ", AnswerAr: "إجابة جديدة."})
	if err != nil || added.ID.IsZero() || added.QuestionAr != "سؤال جديد؟" {
		t.Fatalf("added = %+v, %v", added, err)
	}
	if _, err = console.CreateFAQ(ctx, domain.FAQ{QuestionAr: "؟"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a short question: %v", err)
	}
	added.OnHome, added.QuestionEn, added.AnswerEn = true, "A new question?", "A new answer."
	if _, err = console.UpdateFAQ(ctx, added); err != nil {
		t.Fatal(err)
	}
	if _, err = console.UpdateFAQ(ctx, domain.FAQ{ID: ulid.NewGenerator().New(), QuestionAr: "سؤال؟؟", AnswerAr: "إجابة"}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("updating none: %v", err)
	}
	list, err := console.FAQs(ctx)
	if err != nil || len(list) != 11 || list[1].ID != added.ID || !list[1].OnHome || list[1].QuestionEn != "A new question?" {
		t.Fatalf("after adding, second = %+v (%d), %v", list[1], len(list), err)
	}

	// The site sees the change after its minute.
	if c, _ = site.Content(ctx); len(c.FAQs) != 10 || c.Settings.Email != "" {
		t.Fatalf("change seen before the minute: %d, %+v", len(c.FAQs), c.Settings)
	}
	clk.add(app.CacheTTL)
	if c, _ = site.Content(ctx); len(c.FAQs) != 11 || c.Settings.Email != "hello@iftahonline.com" {
		t.Fatalf("after a minute: %d, %+v", len(c.FAQs), c.Settings)
	}

	if err = console.DeleteFAQ(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	if err = console.DeleteFAQ(ctx, added.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}

	// A failed read keeps the last content.
	broken := app.NewService(app.Deps{DB: appPool, Now: clk.Now})
	if _, err = broken.Content(ctx); err != nil {
		t.Fatal(err)
	}
	appPool.Close()
	clk.add(app.CacheTTL)
	if c, err = broken.Content(ctx); err != nil || len(c.FAQs) != 10 {
		t.Fatalf("after a failed read: %d, %v", len(c.FAQs), err)
	}
	if _, err = app.NewService(app.Deps{DB: appPool, Now: clk.Now}).Content(ctx); err == nil {
		t.Fatal("no content ever read, yet no error")
	}
}

// TestFAQLimit: the FAQ holds at most MaxFAQs questions.
func TestFAQLimit(t *testing.T) {
	ctx := t.Context()
	adminPool := open(t, infra.AdminURL)
	console := app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ulid.NewGenerator()})
	list, err := console.FAQs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var made []ulid.ID
	t.Cleanup(func() {
		for _, id := range made {
			_ = console.DeleteFAQ(t.Context(), id) //nolint:errcheck // cleanup
		}
	})
	for range domain.MaxFAQs - len(list) {
		f, addErr := console.CreateFAQ(ctx, domain.FAQ{Position: 1000, QuestionAr: "سؤال للحد؟", AnswerAr: "إجابة."})
		if addErr != nil {
			t.Fatal(addErr)
		}
		made = append(made, f.ID)
	}
	if _, err = console.CreateFAQ(ctx, domain.FAQ{QuestionAr: "سؤال زائد؟", AnswerAr: "إجابة."}); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("one too many: %v", err)
	}
}
