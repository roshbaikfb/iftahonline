package domain_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestParseVideo(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ link, id string }{
		{"", ""},
		{"  ", ""},
		{"dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=42s", "dQw4w9WgXcQ"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
	} {
		if got, err := domain.ParseVideo(tt.link); err != nil || got != tt.id {
			t.Errorf("%q = %q, %v; want %q", tt.link, got, err, tt.id)
		}
	}
	for _, bad := range []string{
		"https://vimeo.com/123456",
		"https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=short",
		"https://www.youtube.com/channel/UCxyz",
		"javascript:alert(1)",
		"ftp://youtu.be/dQw4w9WgXcQ",
	} {
		if _, err := domain.ParseVideo(bad); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestStory(t *testing.T) {
	t.Parallel()
	s := domain.Story{Ar: "  بدأنا من متجر صغير.\n\nثم رأينا المشكلة.  ", VideoID: "dQw4w9WgXcQ"}
	s.Normalize()
	if err := s.Validate(); err != nil || !strings.HasPrefix(s.Ar, "بدأنا") {
		t.Fatalf("story = %q, %v", s.Ar, err)
	}
	if got := domain.Paragraphs(s.Ar); len(got) != 2 || got[1] != "ثم رأينا المشكلة." {
		t.Fatalf("paragraphs = %q", got)
	}
	if got := domain.Paragraphs("a\r\n\r\n\r\n\r\nb\nc"); len(got) != 2 || got[1] != "b\nc" {
		t.Fatalf("paragraphs = %q", got)
	}
	long := domain.Story{En: strings.Repeat("a", domain.MaxStory+1), VideoID: "bad"}
	if got := fields(t, long.Validate()); strings.Join(got, ",") != "story_en,video_url" {
		t.Fatalf("wrong fields = %v", got)
	}
}

func TestPerson(t *testing.T) {
	t.Parallel()
	ok := func() domain.Person {
		return domain.Person{Kind: domain.Founder, NameAr: "أحمد", RoleAr: "المدير التقني", BioAr: "بنى المنصة.",
			LinkedIn: "https://www.linkedin.com/in/ahmed", X: "https://x.com/ahmed"}
	}
	for _, tt := range []struct {
		name  string
		edit  func(*domain.Person)
		wrong []string
	}{
		{"a founder", func(*domain.Person) {}, nil},
		{"a team member, no links", func(p *domain.Person) { p.Kind, p.LinkedIn, p.X = domain.Team, "", "" }, nil},
		{"no name or role", func(p *domain.Person) { p.NameAr, p.RoleAr = "", "" }, []string{"name_ar", "role_ar"}},
		{"an unknown kind", func(p *domain.Person) { p.Kind = "investor" }, []string{"kind"}},
		{"a long bio", func(p *domain.Person) { p.BioEn = strings.Repeat("b", 601) }, []string{"bio_en"}},
		{"LinkedIn elsewhere", func(p *domain.Person) { p.LinkedIn = "https://linkedin.com.evil.example/in/x" }, []string{"linkedin_url"}},
		{"X over http", func(p *domain.Person) { p.X = "http://x.com/ahmed" }, []string{"x_url"}},
		{"a negative position", func(p *domain.Person) { p.Position = -1 }, []string{"position"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := ok()
			tt.edit(&p)
			if got := fields(t, p.Validate()); strings.Join(got, ",") != strings.Join(tt.wrong, ",") {
				t.Fatalf("wrong fields = %v, want %v", got, tt.wrong)
			}
		})
	}
}

func TestTestimonial(t *testing.T) {
	t.Parallel()
	ok := func() domain.Testimonial {
		return domain.Testimonial{Name: "منى", StoreName: "متجر منى", StoreURL: "https://mona.iftahonline.com", QuoteAr: "قلّت الطلبات الوهمية عندي كثيرًا."}
	}
	for _, tt := range []struct {
		name  string
		edit  func(*domain.Testimonial)
		wrong []string
	}{
		{"a review", func(*domain.Testimonial) {}, nil},
		{"no store", func(r *domain.Testimonial) { r.StoreName, r.StoreURL = "", "" }, nil},
		{"a short quote", func(r *domain.Testimonial) { r.QuoteAr = "ممتاز" }, []string{"quote_ar"}},
		{"a short English quote", func(r *domain.Testimonial) { r.QuoteEn = "Great" }, []string{"quote_en"}},
		{"no name", func(r *domain.Testimonial) { r.Name = "" }, []string{"name"}},
		{"an http store", func(r *domain.Testimonial) { r.StoreURL = "http://mona.example" }, []string{"store_url"}},
		{"a link with credentials", func(r *domain.Testimonial) { r.StoreURL = "https://a:b@mona.example" }, []string{"store_url"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := ok()
			tt.edit(&r)
			if got := fields(t, r.Validate()); strings.Join(got, ",") != strings.Join(tt.wrong, ",") {
				t.Fatalf("wrong fields = %v, want %v", got, tt.wrong)
			}
		})
	}
}

// FuzzParseVideo: any link either is refused or gives an 11-character
// ID the site can put in a URL as is.
func FuzzParseVideo(f *testing.F) {
	for _, s := range []string{"", "dQw4w9WgXcQ", "https://youtu.be/dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&x=%2F",
		"https://youtube.com/shorts/", "http://youtu.be/../../etc", "https://www.youtube.com/embed/a/b/dQw4w9WgXcQ"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, link string) {
		id, err := domain.ParseVideo(link)
		if err != nil {
			return
		}
		if id == "" {
			if strings.TrimSpace(link) != "" {
				t.Fatalf("%q gave no ID and no error", link)
			}
			return
		}
		if len(id) != 11 || strings.ContainsAny(id, "/?#&%.: <>\"'") {
			t.Fatalf("%q gave %q", link, id)
		}
	})
}
