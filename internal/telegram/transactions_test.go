package telegram

import "testing"

func TestFormatPaymentTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-16T10:20:30.123456Z":      "16 Nov 26 10:20",
		"2026-11-16T10:20:30Z":             "16 Nov 26 10:20",
		"2026-11-16T12:20:30.123456+02:00": "16 Nov 26 10:20",
		"2026-11-16T10:20:30.123456":       "16 Nov 26 10:20",
		"2026-11-16 10:20:30.123456":       "16 Nov 26 10:20",
		"2026-11-16 12:20:30+02:00":        "16 Nov 26 10:20",
		"garbage":                          "garbage",
		"gar`bage":                         "garbage",
	} {
		if got := formatPaymentTime(in); got != want {
			t.Errorf("formatPaymentTime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaging(t *testing.T) {
	for _, c := range []struct{ n, pages int }{{0, 1}, {1, 1}, {9, 1}, {10, 1}, {11, 2}, {60, 6}} {
		if got := pageCount(c.n, 10); got != c.pages {
			t.Errorf("pageCount(%d, 10) = %d, want %d", c.n, got, c.pages)
		}
		// every transaction shows exactly once across the pages
		next := 0
		for page := 0; page < pageCount(c.n, 10); page++ {
			start, end := pageBounds(c.n, page, 10)
			if start != next || end < start || end-start > 10 {
				t.Errorf("pageBounds(%d, %d, 10) = [%d, %d), want start %d", c.n, page, start, end, next)
			}
			next = end
		}
		if next != c.n {
			t.Errorf("n=%d: pages cover %d transactions", c.n, next)
		}
	}
	if start, end := pageBounds(25, 7, 10); start != 0 || end != 10 {
		t.Errorf("out-of-range page = [%d, %d), want [0, 10)", start, end)
	}
}
