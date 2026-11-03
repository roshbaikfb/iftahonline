package markdown_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/markdown"
)

func TestHTML(t *testing.T) {
	t.Parallel()
	r := markdown.New()
	for _, tt := range []struct {
		name, src string
		want      []string
		not       []string
	}{
		{"headings move down a level", "# أولًا\n\n## ثانيًا", []string{"<h2 id=", ">أولًا</h2>", "<h3 id="}, []string{"<h1"}},
		{"lists, emphasis and links", "- **مهم**\n- [بوسطة](https://bosta.co)", []string{"<li><strong>مهم</strong></li>", `<a href="https://bosta.co">بوسطة</a>`}, nil},
		{"a table", "| المحافظة | السعر |\n|---|---|\n| القاهرة | 60 |", []string{"<table>", "<td>القاهرة</td>"}, nil},
		{"raw HTML is left out", "قبل <script>alert(1)</script> بعد\n\n<div onclick=\"x()\">x</div>", []string{"قبل"}, []string{"<script", "onclick", "<div"}},
		{"dangerous links are dropped", "[x](javascript:alert(1)) [y](data:text/html,hi) ![z](vbscript:msgbox)", nil, []string{"javascript:", "data:text/html", "vbscript:"}},
		{"text is escaped", "1 < 2 & \"quoted\"", []string{"1 &lt; 2 &amp; &quot;quoted&quot;"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.HTML(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in %s", w, got)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("%q in %s", n, got)
				}
			}
		})
	}
}

// FuzzHTML: whatever the console writes, the site's page gets no script,
// no event handler and no script link.
func FuzzHTML(f *testing.F) {
	for _, s := range []string{"# عنوان", "<script>x</script>", "[a](javascript:x)", "<img src=x onerror=alert(1)>", "`<b>`", "| a |\n|---|\n| <i> |",
		"[a](JaVaScRiPt:alert(1))", "<a href=\"javascript:x\">a</a>", "![](\"onerror=\")", "<<script>>"} {
		f.Add(s)
	}
	r := markdown.New()
	f.Fuzz(func(t *testing.T, src string) {
		got, err := r.HTML(src)
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(got)
		// Text may say "javascript:"; a link or image may not point there.
		for _, bad := range []string{"<script", `href="javascript:`, `src="javascript:`, `href="vbscript:`, `src="vbscript:`,
			"<iframe", "<object", "<embed", "<style", "<svg"} {
			if strings.Contains(low, bad) {
				t.Fatalf("%q renders %q: %s", src, bad, got)
			}
		}
		// An event handler can only come from an HTML tag the renderer
		// wrote itself, never from the source.
		for _, tag := range strings.Split(low, "<")[1:] {
			end := strings.IndexByte(tag, '>')
			if end < 0 {
				continue
			}
			for _, attr := range strings.Fields(tag[:end])[1:] {
				if strings.HasPrefix(attr, "on") {
					t.Fatalf("%q renders an event handler: %s", src, got)
				}
			}
		}
	})
}
