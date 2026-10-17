package server

import (
	"bytes"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/server/views"
)

// MountStorefront registers the storefront's catch-all page. Until the
// store-resolution module exists (domain -> store), every page is
// "store not found".
func MountStorefront(rt Routes) {
	rt.Mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		page := views.ErrorPage(http.StatusNotFound,
			"المتجر غير موجود", "Store not found",
			"لم نعثر على متجر بهذا العنوان.", "We could not find a store at this address.")
		if err := page.Render(r.Context(), &buf); err != nil {
			rt.Problems.Write(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		if _, err := buf.WriteTo(w); err != nil {
			rt.Log.DebugContext(r.Context(), "storefront: client went away", "err", err)
		}
	})
}
