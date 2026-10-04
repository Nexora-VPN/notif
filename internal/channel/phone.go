package channel

import (
	"regexp"
	"strings"
)

// Phone numbers as operators type them — "+98 912 000 0000", "0912…",
// "912…", "8 (912) …" — read into the forms providers take. A number that
// cannot be read is no address, never a guess.

var iranMobile = regexp.MustCompile(`^\+989\d{9}$`)

// E164 is the number as +<country><number>, or "" when it cannot be read.
func E164(raw string) string {
	raw = strings.TrimSpace(raw)
	plus := strings.HasPrefix(raw, "+")
	var b strings.Builder
	for _, r := range strings.TrimPrefix(raw, "+") {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '۰' && r <= '۹': // Persian digits
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩': // Arabic-Indic digits
			b.WriteRune('0' + (r - '٠'))
		case strings.ContainsRune(" -().‌", r):
		default:
			return ""
		}
	}
	d := b.String()
	switch {
	case plus:
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	case len(d) == 11 && strings.HasPrefix(d, "09"): // 0912…, Iran
		d = "98" + d[1:]
	case len(d) == 10 && strings.HasPrefix(d, "9"): // 912…, Iran
		d = "98" + d
	case len(d) == 11 && strings.HasPrefix(d, "8"): // 8 912 …, Russia
		d = "7" + d[1:]
	}
	if len(d) < 8 || len(d) > 15 || d[0] == '0' {
		return ""
	}
	return "+" + d
}

// IranLocal is an Iranian mobile number as 09…, or "" for any other.
func IranLocal(raw string) string {
	e := E164(raw)
	if !iranMobile.MatchString(e) {
		return ""
	}
	return "0" + e[3:]
}
