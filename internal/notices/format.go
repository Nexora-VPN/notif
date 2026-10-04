package notices

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// How figures read in each language: dates in the calendar the admin
// chose (the Persian one in Persian by default), traffic in GB or MB, and
// Persian digits in Persian.

// Jalali is the Persian (Solar Hijri) date of a Gregorian one — the
// arithmetic of the 33-year cycle, exact for 1800–2400.
func Jalali(t time.Time) (jy, jm, jd int) {
	gy, gmm, gd := t.Date()
	gm := int(gmm)
	gdm := [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 + gd + gdm[gm-1]
	jy = -1595 + 33*(days/12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	if days < 186 {
		return jy, 1 + days/31, 1 + days%31
	}
	return jy, 7 + (days-186)/30, 1 + (days-186)%30
}

var faDigits = strings.NewReplacer("0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴", "5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹", ".", "٫")

// Digits writes a figure's digits the language's way.
func Digits(lang, s string) string {
	if lang == "fa" {
		return faDigits.Replace(s)
	}
	return s
}

var months = map[string][12]string{
	"en": {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	"ru": {"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"},
}

// Date writes a day in a language and calendar ("auto", "jalali",
// "gregorian"), in loc.
func Date(unix int64, lang, calendar string, loc *time.Location) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).In(loc)
	if calendar == "jalali" || calendar == "auto" && lang == "fa" {
		y, m, d := Jalali(t)
		return Digits(lang, fmt.Sprintf("%d/%02d/%02d", y, m, d))
	}
	switch lang {
	case "fa":
		return Digits(lang, t.Format("2006/01/02"))
	case "zh":
		return fmt.Sprintf("%d年%d月%d日", t.Year(), t.Month(), t.Day())
	case "ru":
		return fmt.Sprintf("%d %s %d", t.Day(), months["ru"][t.Month()-1], t.Year())
	default:
		return fmt.Sprintf("%s %d, %d", months["en"][t.Month()-1], t.Day(), t.Year())
	}
}

var units = map[string][2]string{
	"en": {"GB", "MB"},
	"fa": {"گیگابایت", "مگابایت"},
	"ru": {"ГБ", "МБ"},
	"zh": {"GB", "MB"},
}

// Traffic writes an amount of bytes: GB with one decimal from a gigabyte,
// MB below it.
func Traffic(bytes int64, lang string) string {
	if bytes < 0 {
		bytes = 0
	}
	u, ok := units[lang]
	if !ok {
		u = units["en"]
	}
	const gb, mb = 1 << 30, 1 << 20
	var n string
	unit := u[0]
	if bytes >= gb {
		n = strconv.FormatFloat(float64(bytes)/gb, 'f', 1, 64)
		n = strings.TrimSuffix(n, ".0")
	} else {
		n = strconv.FormatInt(bytes/mb, 10)
		unit = u[1]
	}
	if lang == "ru" {
		n = strings.ReplaceAll(n, ".", ",")
	}
	return Digits(lang, n) + " " + unit
}
