package arabic

import (
	"strconv"
	"time"
)

var (
	months   = [...]string{"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"} //nolint:gochecknoglobals // constant table
	weekdays = [...]string{"الأحد", "الاثنين", "الثلاثاء", "الأربعاء", "الخميس", "الجمعة", "السبت"}                                       //nolint:gochecknoglobals // constant table
)

// Date is t's day as Arabic writes it, in t's location: "3 نوفمبر 2026".
func Date(t time.Time) string {
	return strconv.Itoa(t.Day()) + " " + months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}

// Moment is t's day and time in t's location, the year left out: "الأحد 8
// نوفمبر 2:30 م".
func Moment(t time.Time) string {
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	half := "ص"
	if t.Hour() >= 12 {
		half = "م"
	}
	return weekdays[t.Weekday()] + " " + strconv.Itoa(t.Day()) + " " + months[t.Month()-1] + " " +
		strconv.Itoa(h) + ":" + t.Format("04") + " " + half
}
