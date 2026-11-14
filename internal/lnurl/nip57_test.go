package lnurl

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestCheckZapRequest(t *testing.T) {
	pub := strings.Repeat("ab", 32)
	ok := nostr.Event{Kind: 9734, Tags: nostr.Tags{
		{"relays", "wss://relay.example"},
		{"amount", "21000"},
		{"p", pub},
		{"e", strings.Repeat("cd", 32)},
	}}
	if err := checkZapRequest(ok, 21000); err != nil {
		t.Fatalf("valid zap request refused: %v", err)
	}
	bad := map[string]nostr.Event{
		"wrong kind":         {Kind: 1, Tags: ok.Tags},
		"no p tag":           {Kind: 9734, Tags: nostr.Tags{{"preimage", pub}}},
		"two p tags":         {Kind: 9734, Tags: nostr.Tags{{"p", pub}, {"p", pub}}},
		"p not a pubkey":     {Kind: 9734, Tags: nostr.Tags{{"p", "x"}}},
		"two e tags":         {Kind: 9734, Tags: nostr.Tags{{"p", pub}, {"e", "1"}, {"e", "2"}}},
		"amount mismatch":    {Kind: 9734, Tags: nostr.Tags{{"p", pub}, {"amount", "1000"}}},
		"prefix-named p tag": {Kind: 9734, Tags: nostr.Tags{{"proxy", pub}}},
	}
	for name, ev := range bad {
		if err := checkZapRequest(ev, 21000); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the receipt must take the real p tag, not a tag whose name starts with p
	tags := nostr.Tags{{"preimage", "fake"}, {"p", pub}}
	if got := zapTags(tags, "p"); len(got) != 1 || got[0][1] != pub {
		t.Errorf("zapTags(p) = %v", got)
	}
}
