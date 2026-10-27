package web

import (
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
)

// colorOptions are the option names that hold colors.
var colorOptions = map[string]bool{ //nolint:gochecknoglobals // constant lookup table
	"اللون": true, "لون": true, "الالوان": true, "الوان": true, "color": true, "colour": true, "colors": true,
}

// swatches maps common color names, Arabic and English, to the swatch
// class site.css paints (sw-<name>): the CSP allows no inline styles, so
// the colors live in the stylesheet. A colour the merchant set wins
// (productView); without one an unknown name shows as a text button.
var swatches = map[string]string{ //nolint:gochecknoglobals // constant lookup table
	"اسود": "black", "black": "black",
	"ابيض": "white", "white": "white",
	"احمر": "red", "red": "red",
	"ازرق": "blue", "blue": "blue",
	"كحلي": "navy", "navy": "navy",
	"اخضر": "green", "green": "green",
	"زيتي": "olive", "olive": "olive",
	"رمادي": "gray", "gray": "gray", "grey": "gray",
	"بني": "brown", "brown": "brown",
	"بيج": "beige", "beige": "beige",
	"وردي": "pink", "بمبي": "pink", "pink": "pink",
	"بنفسجي": "purple", "purple": "purple",
	"اصفر": "yellow", "yellow": "yellow",
	"برتقالي": "orange", "orange": "orange",
	"ذهبي": "gold", "gold": "gold",
	"فضي": "silver", "silver": "silver",
	"سماوي": "sky", "لبني": "sky", "sky": "sky",
	"نبيتي": "maroon", "عنابي": "maroon", "maroon": "maroon", "burgundy": "maroon",
	"كريمي": "cream", "cream": "cream",
	"موف": "mauve", "mauve": "mauve",
	"فيروزي": "teal", "turquoise": "teal", "teal": "teal",
}

// isColorOption reports whether an option is the product's color.
func isColorOption(name string) bool { return colorOptions[colorKey(name)] }

// swatch is the swatch class of a color name, or "" when we do not know
// the color.
func swatch(value string) string {
	if c := swatches[colorKey(value)]; c != "" {
		return "sw-" + c
	}
	return ""
}

// isHex reports whether s is "#rrggbb" in lower-case hex, the only
// merchant colour form written into a stylesheet.
func isHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range []byte(s[1:]) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// colorKey matches names however they are written: "أبيض" and "ابيض",
// "Navy" and "navy".
func colorKey(s string) string { return strings.ToLower(arabic.NormalizeSearch(s)) }
