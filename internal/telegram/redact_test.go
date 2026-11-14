package telegram

import "testing"

func TestRedactSecrets(t *testing.T) {
	cases := map[string]string{
		"/nostr add nsec1abcdef":                     "/nostr add [redacted]",
		"/lnurl LNURL1DP68GURN8GHJ7":                 "/lnurl [redacted]",
		"lightning:lnurl1dp68gurn":                   "[redacted]",
		"/lnurl https://example.com/w?k1=00ff&tag=w": "/lnurl [redacted]",
		"/tip 21 thanks":                             "/tip 21 thanks",
	}
	for in, want := range cases {
		if got := redactSecrets(in); got != want {
			t.Errorf("redactSecrets(%q) = %q, want %q", in, got, want)
		}
	}
}
