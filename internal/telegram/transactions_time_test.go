package telegram

import "testing"

func TestFormatPaymentTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-16T10:20:30.123456Z":      "16 Nov 26 10:20",
		"2026-11-16T10:20:30Z":             "16 Nov 26 10:20",
		"2026-11-16T12:20:30.123456+02:00": "16 Nov 26 10:20",
		"2026-11-16T10:20:30.123456":       "16 Nov 26 10:20",
		"garbage":                          "garbage",
	} {
		if got := formatPaymentTime(in); got != want {
			t.Errorf("formatPaymentTime(%q) = %q, want %q", in, got, want)
		}
	}
}
