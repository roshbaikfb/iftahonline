// Package geo lists the first-level administrative areas (governorates,
// regions, emirates) of the countries we sell in, for delivery
// addresses. Codes are ISO 3166-2, so they are stable in stored orders
// and exports; names are for display in Arabic and English.
//
// The storefront renders the list in the order form and orders validate
// against it. Cities are free text: courier coverage, not this list,
// decides what a city is.
package geo

import (
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Country is an ISO 3166-1 alpha-2 code.
type Country string

// Supported countries: Egypt and the GCC (as platform/phone).
const (
	EG Country = "EG"
	SA Country = "SA"
	AE Country = "AE"
	KW Country = "KW"
	QA Country = "QA"
	BH Country = "BH"
	OM Country = "OM"
)

// Area is a governorate, region or emirate.
type Area struct {
	Code   string // ISO 3166-2, e.g. "EG-C"
	NameAR string
	NameEN string
}

// Name returns the area's name for lang ("ar" or "en"; anything else is
// Arabic, the default storefront language).
func (a Area) Name(lang string) string {
	if lang == "en" {
		return a.NameEN
	}
	return a.NameAR
}

// Areas returns country's areas in Arabic alphabetical order, the order a
// shopper scans a list in; nil for an unsupported country. The slice is
// new on every call.
func Areas(c Country) []Area {
	switch c {
	case EG:
		return []Area{
			{"EG-LX", "الأقصر", "Luxor"},
			{"EG-ALX", "الإسكندرية", "Alexandria"},
			{"EG-IS", "الإسماعيلية", "Ismailia"},
			{"EG-BA", "البحر الأحمر", "Red Sea"},
			{"EG-BH", "البحيرة", "Beheira"},
			{"EG-GZ", "الجيزة", "Giza"},
			{"EG-DK", "الدقهلية", "Dakahlia"},
			{"EG-SUZ", "السويس", "Suez"},
			{"EG-SHR", "الشرقية", "Sharqia"},
			{"EG-GH", "الغربية", "Gharbia"},
			{"EG-FYM", "الفيوم", "Faiyum"},
			{"EG-C", "القاهرة", "Cairo"},
			{"EG-KB", "القليوبية", "Qalyubia"},
			{"EG-MN", "المنيا", "Minya"},
			{"EG-MNF", "المنوفية", "Monufia"},
			{"EG-WAD", "الوادي الجديد", "New Valley"},
			{"EG-ASN", "أسوان", "Aswan"},
			{"EG-AST", "أسيوط", "Asyut"},
			{"EG-BNS", "بني سويف", "Beni Suef"},
			{"EG-PTS", "بورسعيد", "Port Said"},
			{"EG-JS", "جنوب سيناء", "South Sinai"},
			{"EG-DT", "دمياط", "Damietta"},
			{"EG-SHG", "سوهاج", "Sohag"},
			{"EG-SIN", "شمال سيناء", "North Sinai"},
			{"EG-KN", "قنا", "Qena"},
			{"EG-KFS", "كفر الشيخ", "Kafr El Sheikh"},
			{"EG-MT", "مطروح", "Matrouh"},
		}
	case SA:
		return []Area{
			{"SA-11", "الباحة", "Al Bahah"},
			{"SA-12", "الجوف", "Al Jawf"},
			{"SA-08", "الحدود الشمالية", "Northern Borders"},
			{"SA-01", "الرياض", "Riyadh"},
			{"SA-05", "القصيم", "Al Qassim"},
			{"SA-03", "المدينة المنورة", "Madinah"},
			{"SA-04", "المنطقة الشرقية", "Eastern Province"},
			{"SA-07", "تبوك", "Tabuk"},
			{"SA-09", "جازان", "Jazan"},
			{"SA-06", "حائل", "Hail"},
			{"SA-14", "عسير", "Asir"},
			{"SA-02", "مكة المكرمة", "Makkah"},
			{"SA-10", "نجران", "Najran"},
		}
	case AE:
		return []Area{
			{"AE-FU", "الفجيرة", "Fujairah"},
			{"AE-SH", "الشارقة", "Sharjah"},
			{"AE-UQ", "أم القيوين", "Umm Al Quwain"},
			{"AE-AZ", "أبوظبي", "Abu Dhabi"},
			{"AE-DU", "دبي", "Dubai"},
			{"AE-RK", "رأس الخيمة", "Ras Al Khaimah"},
			{"AE-AJ", "عجمان", "Ajman"},
		}
	case KW:
		return []Area{
			{"KW-AH", "الأحمدي", "Al Ahmadi"},
			{"KW-JA", "الجهراء", "Al Jahra"},
			{"KW-KU", "العاصمة", "Capital"},
			{"KW-FA", "الفروانية", "Al Farwaniyah"},
			{"KW-HA", "حولي", "Hawalli"},
			{"KW-MU", "مبارك الكبير", "Mubarak Al-Kabeer"},
		}
	case QA:
		return []Area{
			{"QA-DA", "الدوحة", "Doha"},
			{"QA-RA", "الريان", "Al Rayyan"},
			{"QA-SH", "الشحانية", "Al Shahaniya"},
			{"QA-MS", "الشمال", "Al Shamal"},
			{"QA-ZA", "الضعاين", "Al Daayen"},
			{"QA-KH", "الخور", "Al Khor"},
			{"QA-WA", "الوكرة", "Al Wakrah"},
			{"QA-US", "أم صلال", "Umm Salal"},
		}
	case BH:
		return []Area{
			{"BH-14", "الجنوبية", "Southern"},
			{"BH-17", "الشمالية", "Northern"},
			{"BH-13", "العاصمة", "Capital"},
			{"BH-15", "المحرق", "Muharraq"},
		}
	case OM:
		return []Area{
			{"OM-BU", "البريمي", "Al Buraimi"},
			{"OM-DA", "الداخلية", "Ad Dakhiliyah"},
			{"OM-ZA", "الظاهرة", "Ad Dhahirah"},
			{"OM-WU", "الوسطى", "Al Wusta"},
			{"OM-BJ", "جنوب الباطنة", "South Al Batinah"},
			{"OM-SJ", "جنوب الشرقية", "South Ash Sharqiyah"},
			{"OM-BS", "شمال الباطنة", "North Al Batinah"},
			{"OM-SS", "شمال الشرقية", "North Ash Sharqiyah"},
			{"OM-ZU", "ظفار", "Dhofar"},
			{"OM-MA", "مسقط", "Muscat"},
			{"OM-MU", "مسندم", "Musandam"},
		}
	}
	return nil
}

// Lookup returns the area with code in country. Codes are matched
// exactly after trimming spaces and upper-casing (external input).
func Lookup(c Country, code string) (Area, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !strings.HasPrefix(code, string(c)+"-") {
		return Area{}, false
	}
	for _, a := range Areas(c) {
		if a.Code == code {
			return a, true
		}
	}
	return Area{}, false
}

// CountryOf returns the country whose currency a store sells in (one
// currency per store, ADR-0011), so the order form needs no extra store
// setting.
func CountryOf(cur money.Currency) (Country, bool) {
	switch cur {
	case money.EGP:
		return EG, true
	case money.SAR:
		return SA, true
	case money.AED:
		return AE, true
	}
	return "", false
}
