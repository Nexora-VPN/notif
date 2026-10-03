package settings

import (
	"testing"
	"time"
)

func TestQuietHours(t *testing.T) {
	d := Delivery{TimeZone: "Asia/Tehran", QuietEnabled: true, QuietFrom: "22:00", QuietTo: "08:00"}
	tehran, _ := time.LoadLocation("Asia/Tehran")
	at := func(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, tehran) }
	cases := []struct {
		t    time.Time
		want time.Time
	}{
		{at(4, 21, 59), time.Time{}},
		{at(4, 22, 0), at(5, 8, 0)},
		{at(5, 3, 0), at(5, 8, 0)},
		{at(5, 8, 0), time.Time{}},
	}
	for _, c := range cases {
		if got := d.QuietUntil(c.t); !got.Equal(c.want) {
			t.Errorf("%v: %v, want %v", c.t, got, c.want)
		}
	}
	day := Delivery{TimeZone: "UTC", QuietEnabled: true, QuietFrom: "01:00", QuietTo: "06:00"}
	if got := day.QuietUntil(time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)); got.Hour() != 6 {
		t.Errorf("within a day: %v", got)
	}
	if !(Delivery{QuietFrom: "22:00", QuietTo: "08:00"}).QuietUntil(at(4, 23, 0)).IsZero() {
		t.Error("quiet hours that are off held a notice")
	}
	bad := []Delivery{
		{TimeZone: "Mars/Base", Language: "en", RetentionDays: 1},
		{QuietFrom: "25:00", QuietTo: "08:00", Language: "en", RetentionDays: 1},
		{QuietEnabled: true, QuietFrom: "08:00", QuietTo: "08:00", Language: "en", RetentionDays: 1},
		{QuietFrom: "22:00", QuietTo: "08:00", Language: "de", RetentionDays: 1},
	}
	for _, b := range bad {
		if b.Check() == nil {
			t.Errorf("%+v was accepted", b)
		}
	}
}
