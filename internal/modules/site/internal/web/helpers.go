package web

import (
	"strconv"
	"strings"
)

// stepNumber is a step's number, from 1.
func stepNumber(i int) string { return strconv.Itoa(i + 1) }

// teaserPlans are the home page's three: the free plan, the recommended
// subscription, and pay as you sell.
func teaserPlans(pr pricing) []planView {
	var free, rec, pays []planView
	for _, p := range pr.Plans {
		switch {
		case p.Free:
			free = append(free, p)
		case p.Recommended:
			rec = append(rec, p)
		case p.Pays:
			pays = append(pays, p)
		}
	}
	return append(append(free, rec...), pays...)
}

// joinLines is a plan's first n bullets in one sentence.
func joinLines(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "، ")
}

// freePrice is nothing, in the page's currency.
func freePrice(pl planView) string {
	if strings.HasPrefix(pl.Monthly, "$") {
		return "$0"
	}
	return "0 ج.م"
}

// planCTA is a plan card's button.
func planCTA(pl planView) string {
	switch {
	case pl.Free:
		return "ابدأ مجانًا"
	case pl.Pays:
		return "ابدأ بلا اشتراك"
	case pl.TrialDays > 0:
		return "جرّبها مجانًا"
	default:
		return "اشترك الآن"
	}
}

// trialText is a trial's length in Arabic: "14 يومًا".
func trialText(days int) string {
	switch {
	case days == 1:
		return "يومًا واحدًا"
	case days == 2:
		return "يومين"
	case days >= 3 && days <= 10:
		return strconv.Itoa(days) + " أيام"
	default:
		return strconv.Itoa(days) + " يومًا"
	}
}

// displayPhone writes an E.164 number for people: +201153450716 →
// "+20 115 345 0716".
func displayPhone(e164 string) string {
	d := strings.TrimPrefix(e164, "+")
	if strings.HasPrefix(d, "20") && len(d) == 12 {
		return "+20 " + d[2:5] + " " + d[5:8] + " " + d[8:]
	}
	return e164
}

// favicon is the brand mark: the bag on navy.
const favicon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="8" fill="#0f2e52"/>` +
	`<g fill="none" stroke="#fff" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" transform="translate(4 4)">` +
	`<path d="M4 9h16l-1.5 10.5a1 1 0 0 1-1 .5H6.5a1 1 0 0 1-1-.5z"/><path d="M9 9V7a3 3 0 0 1 6 0v2"/></g></svg>`

// iconSVG is a stroke icon by name, decorative (the link or button around
// it carries the label).
func iconSVG(name string) string {
	paths := map[string]string{
		"bag":      `<path d="M4 9h16l-1.5 10.5a1 1 0 0 1-1 .5H6.5a1 1 0 0 1-1-.5z"/><path d="M9 9V7a3 3 0 0 1 6 0v2"/>`,
		"chat":     `<path d="M21 12a9 9 0 0 1-13.5 7.8L3 21l1.2-4.4A9 9 0 1 1 21 12z"/>`,
		"check":    `<path d="M5 13l4 4L19 7"/>`,
		"menu":     `<path d="M4 7h16M4 12h16M4 17h16"/>`,
		"lock":     `<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>`,
		"people":   `<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0M16 4.5a3.5 3.5 0 0 1 0 7M21.5 20a6.5 6.5 0 0 0-4-6"/>`,
		"download": `<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>`,
	}
	return `<svg class="i" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + paths[name] + `</svg>`
}
