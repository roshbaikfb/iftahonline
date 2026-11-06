// Package qr draws QR codes as SVG, for an authenticator app to scan
// (the console's team invitations, the dashboard's account page).
package qr

import (
	"fmt"
	"strings"

	rscqr "rsc.io/qr"
)

// SVG draws text as a QR code in SVG: one path of dark modules with a
// quiet zone of four.
func SVG(text string) (string, error) {
	c, err := rscqr.Encode(text, rscqr.M)
	if err != nil {
		return "", fmt.Errorf("qr: %w", err)
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := range c.Size {
		for x := range c.Size {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
