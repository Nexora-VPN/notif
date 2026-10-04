package notices

import (
	"testing"
	"time"
)

func TestFigures(t *testing.T) {
	for _, c := range []struct {
		g       string
		y, m, d int
	}{{"2026-10-04", 1405, 7, 12}, {"2025-03-21", 1404, 1, 1}, {"2024-03-20", 1403, 1, 1}, {"2026-03-20", 1404, 12, 29}, {"2023-12-31", 1402, 10, 10}} {
		g, _ := time.Parse("2006-01-02", c.g)
		y, m, d := Jalali(g)
		if y != c.y || m != c.m || d != c.d {
			t.Errorf("%s = %d/%d/%d, want %d/%d/%d", c.g, y, m, d, c.y, c.m, c.d)
		}
	}
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).Unix()
	for _, c := range []struct{ lang, cal, want string }{
		{"fa", "auto", "۱۴۰۵/۰۷/۱۲"},
		{"fa", "gregorian", "۲۰۲۶/۱۰/۰۴"},
		{"en", "auto", "Oct 4, 2026"},
		{"en", "jalali", "1405/07/12"},
		{"ru", "auto", "4 октября 2026"},
		{"zh", "auto", "2026年10月4日"},
	} {
		if got := Date(at, c.lang, c.cal, time.UTC); got != c.want {
			t.Errorf("Date %s %s = %q, want %q", c.lang, c.cal, got, c.want)
		}
	}
	for _, c := range []struct {
		b          int64
		lang, want string
	}{{50 << 30, "en", "50 GB"}, {1610612736, "fa", "۱٫۵ گیگابایت"}, {1610612736, "ru", "1,5 ГБ"}, {300 << 20, "zh", "300 MB"}, {-5, "en", "0 MB"}} {
		if got := Traffic(c.b, c.lang); got != c.want {
			t.Errorf("Traffic(%d, %s) = %q, want %q", c.b, c.lang, got, c.want)
		}
	}
}
