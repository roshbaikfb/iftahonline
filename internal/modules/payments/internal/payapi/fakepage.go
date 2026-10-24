package payapi

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
)

// FakeCheckoutPath is where the fake gateway's hosted pages are served
// (development only, ADR-0030).
const FakeCheckoutPath = "/v1/fake-gateway/checkout/"

const fakePageHTML = `<!doctype html>
<html lang="ar" dir="rtl">
<head><meta charset="utf-8">{{if .Back}}<meta http-equiv="refresh" content="0;url={{.Back}}">{{end}}<meta name="viewport" content="width=device-width, initial-scale=1"><title>بوابة دفع تجريبية</title></head>
<body>
<h1>بوابة دفع تجريبية</h1>
<p>هذه الصفحة للتجربة فقط: لا تُدخل أي بيانات بطاقة.</p>
<p>المبلغ: <strong dir="ltr">{{.Amount}}</strong></p>
{{if .Back}}
<p>تم. <a href="{{.Back}}">العودة إلى المتجر</a></p>
{{else if .Pending}}
<form method="post"><button name="answer" value="pay">ادفع</button> <button name="answer" value="decline">ارفض</button></form>
{{else}}
<p>تمت الإجابة على هذا الدفع.</p>
{{end}}
</body>
</html>
`

var fakePage = template.Must(template.New("fake").Parse(fakePageHTML)) //nolint:gochecknoglobals // parsed once, read-only

// fakePageCSP lets the page post its own form and nothing else. The
// shopper goes back with a meta refresh, not a redirect, so form-action
// never has to allow the store's host.
const fakePageCSP = "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

func writeFakePage(w http.ResponseWriter, data map[string]any) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", fakePageCSP)
	_ = fakePage.Execute(w, data) //nolint:errcheck // the response is already started
}

// MountFakeGateway serves the fake gateway's hosted page: the shopper
// pays or declines, then goes back where the payment said. Never mounted
// in production (the fake gateway is refused there).
func MountFakeGateway(mux *http.ServeMux, g *fake.Gateway) {
	mux.HandleFunc("GET "+FakeCheckoutPath+"{ref}", func(w http.ResponseWriter, r *http.Request) {
		page, ok := g.PageOf(r.PathValue("ref"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeFakePage(w, map[string]any{"Amount": amountText(page), "Pending": page.Pending})
	})
	mux.HandleFunc("POST "+FakeCheckoutPath+"{ref}", func(w http.ResponseWriter, r *http.Request) {
		ref := r.PathValue("ref")
		page, ok := g.PageOf(ref)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if err := g.Answer(ref, r.PostFormValue("answer") == "pay"); err != nil {
			http.Error(w, "already answered", http.StatusConflict)
			return
		}
		writeFakePage(w, map[string]any{"Amount": amountText(page), "Back": page.Redirect})
	})
}

func amountText(p fake.Page) string {
	return fmt.Sprintf("%d.%02d %s", p.Amount.Amount()/100, p.Amount.Amount()%100, p.Amount.Currency())
}
