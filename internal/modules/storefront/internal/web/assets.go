package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

//go:embed assets/dist/site.css assets/dist/checkout.css assets/dist/app.js assets/dist/checkout.js assets/fonts/*.woff2
var assetFiles embed.FS

// AssetsPrefix is where static files are served, on every store host.
const AssetsPrefix = "/assets/"

// asset is one static file served under a content-hashed name, so it can
// be cached forever (a new build gets a new name).
type asset struct {
	data        []byte
	gz          []byte // the script and stylesheets gzipped once at start-up; nil for fonts (already compressed)
	contentType string
}

// Assets are the storefront's embedded static files: the scripts, the
// fonts and the cart and checkout pages' stylesheet, served from our
// origin (CSP 'self'), and the base stylesheet, which is inlined into
// pages.
type Assets struct {
	baseCSS     string
	jsURL       string
	checkoutURL string            // checkout.css: the cart, checkout and thank-you pages only
	checkoutJS  string            // checkout.js: the checkout page only
	fonts       map[string]string // "cairo-arabic-400" → URL
	files       map[string]asset  // "app.3f2a9c1b.js" → file
}

// LoadAssets reads the embedded files and names them by content hash.
func LoadAssets() (*Assets, error) {
	css, err := assetFiles.ReadFile("assets/dist/site.css")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	a := &Assets{baseCSS: strings.TrimSpace(string(css)), fonts: map[string]string{}, files: map[string]asset{}}
	js, err := assetFiles.ReadFile("assets/dist/app.js")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	a.jsURL = a.add("app", ".js", "text/javascript; charset=utf-8", js)
	checkout, err := assetFiles.ReadFile("assets/dist/checkout.css")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	a.checkoutURL = a.add("checkout", ".css", "text/css; charset=utf-8", checkout)
	checkoutJS, err := assetFiles.ReadFile("assets/dist/checkout.js")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	a.checkoutJS = a.add("checkout", ".js", "text/javascript; charset=utf-8", checkoutJS)
	fonts, err := fs.Glob(assetFiles, "assets/fonts/*.woff2")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	for _, f := range fonts {
		b, readErr := assetFiles.ReadFile(f)
		if readErr != nil {
			return nil, fmt.Errorf("web: %w", readErr)
		}
		name := strings.TrimSuffix(path.Base(f), ".woff2")
		a.fonts[name] = a.add(name, ".woff2", "font/woff2", b)
	}
	return a, nil
}

func (a *Assets) add(name, ext, contentType string, data []byte) string {
	sum := sha256.Sum256(data)
	file := name + "." + hex.EncodeToString(sum[:4]) + ext
	f := asset{data: data, contentType: contentType}
	if strings.HasPrefix(contentType, "text/") {
		f.gz = compress(data)
	}
	a.files[file] = f
	return AssetsPrefix + file
}

// ServeHTTP serves a hashed file with a year-long immutable cache, or 404.
func (a *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, ok := a.files[strings.TrimPrefix(r.URL.Path, AssetsPrefix)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	body := f.data
	if f.gz != nil {
		h.Set("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = f.gz
		}
	}
	if _, err := w.Write(body); err != nil {
		return // the client went away
	}
}

// fontFamilies maps a theme font to its file prefix and CSS family name.
// The system font downloads nothing. ascii marks a family whose Arabic
// files also hold ASCII: an Arabic page then needs no Latin file.
var fontFamilies = map[domain.Font]struct { //nolint:gochecknoglobals // constant lookup table
	file, family string
	ascii        bool
}{
	domain.FontCairo:      {"cairo", "Cairo", false},
	domain.FontTajawal:    {"tajawal", "Tajawal", false},
	domain.FontPlexArabic: {"iftah-sans-arabic", "Iftah Sans Arabic", true}, // IBM Plex Sans Arabic, subset and renamed (scripts/build-iftah-sans.py)
	domain.FontNotoKufi:   {"noto-kufi-arabic", "Noto Kufi Arabic", false},
}

// Unicode ranges of the two subsets, as Fontsource builds them.
const (
	rangeArabic = "U+0600-06FF,U+0750-077F,U+0870-088E,U+0890-0891,U+0897-08E1,U+08E3-08FF,U+200C-200E,U+2010-2011,U+204F,U+2E41,U+FB50-FDFF,U+FE70-FE74,U+FE76-FEFC"
	rangeASCII  = ",U+0020-007E,U+00A0-00BF,U+00D7,U+00F7,U+2010,U+2013-2015,U+2018-201A,U+201C-201E,U+2020-2022,U+2026,U+2030,U+2039-203A,U+20AC,U+2122,U+2212"
	rangeLatin  = "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD"
)

// themeCSS is a store's stylesheet: its font faces and palette as custom
// properties, then the base rules. Every value comes from validated
// theme settings (hex colors, enum font and radius) or from us, so
// nothing a merchant typed reaches CSS as text.
func (a *Assets) themeCSS(t domain.ThemeSettings) string {
	var b strings.Builder
	family := "system-ui"
	if f, ok := fontFamilies[t.Font]; ok {
		family = `"` + f.family + `"`
		arabic := rangeArabic
		if f.ascii {
			arabic += rangeASCII
		}
		for _, w := range []string{"400", "700"} {
			// Where ranges overlap the face declared last wins, so the
			// Arabic face comes after the Latin one.
			for _, sub := range []struct{ name, rng string }{{"latin", rangeLatin}, {"arabic", arabic}} {
				fmt.Fprintf(&b, "@font-face{font-family:%q;font-style:normal;font-weight:%s;font-display:swap;src:url(%s) format(\"woff2\");unicode-range:%s}",
					f.family, w, a.fonts[f.file+"-"+sub.name+"-"+w], sub.rng)
			}
		}
	}
	radius := map[domain.Radius]string{domain.RadiusNone: "0", domain.RadiusSmall: ".25rem", domain.RadiusMedium: ".5rem", domain.RadiusLarge: "1rem"}[t.Radius]
	if radius == "" {
		radius = ".5rem"
	}
	c := t.Colors
	fmt.Fprintf(&b, ":root{--font:%s;--radius:%s;--c-primary:%s;--c-accent:%s;--c-bg:%s;--c-text:%s;--c-on-primary:%s;--c-on-accent:%s}",
		family, radius, c.Primary, c.Accent, c.Background, c.Text, domain.OnColor(c.Primary), domain.OnColor(c.Accent))
	b.WriteString(a.baseCSS)
	return b.String()
}

// fontPreload is the font files the first paint needs: both weights in
// the shopper's script. Headings and labels are bold, and a bold face
// found only once the stylesheet is parsed costs the largest paint a
// round trip.
func (a *Assets) fontPreload(t domain.ThemeSettings, l Locale) []string {
	f, ok := fontFamilies[t.Font]
	if !ok {
		return nil
	}
	sub := "latin"
	if l == Arabic || f.ascii {
		sub = "arabic"
	}
	return []string{a.fonts[f.file+"-"+sub+"-400"], a.fonts[f.file+"-"+sub+"-700"]}
}

// styleHash is the CSP source that allows exactly this inline style.
func styleHash(css string) string {
	sum := sha256.Sum256([]byte(css))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}
