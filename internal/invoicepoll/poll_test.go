package invoicepoll

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWatchCallsHookOnceWhenPaid(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	checks, calls := 0, 0
	check := func() (Status, error) {
		checks++
		switch checks {
		case 1:
			return Status{}, errors.New("lnbits hiccup")
		case 2:
			return Status{Paid: false}, nil
		}
		return Status{Paid: true, WalletID: "w1", AmountMsat: 21000}, nil
	}
	PaidHook = func(walletID, paymentHash string, amountMsat int64) {
		if walletID != "w1" || paymentHash != hash || amountMsat != 21000 {
			t.Errorf("hook got %s %s %d", walletID, paymentHash, amountMsat)
		}
		calls++
	}
	defer func() { PaidHook = nil }()

	Watch(hash, time.Now().Add(5*time.Second), time.Millisecond, check)
	if checks != 3 || calls != 1 {
		t.Fatalf("checks=%d hook calls=%d, want 3 and 1", checks, calls)
	}

	// an expired deadline never checks
	checks, calls = 0, 0
	Watch(hash, time.Now().Add(-time.Second), time.Millisecond, check)
	if checks != 0 || calls != 0 {
		t.Fatalf("expired watch checked %d times, hook calls=%d", checks, calls)
	}

	// without a free slot nothing is watched
	for len(slots) < cap(slots) {
		slots <- struct{}{}
	}
	Watch(hash, time.Now().Add(5*time.Second), time.Millisecond, check)
	for len(slots) > 0 {
		<-slots
	}
	if checks != 0 || calls != 0 {
		t.Fatalf("watch without a slot checked %d times, hook calls=%d", checks, calls)
	}
}

func TestDeadline(t *testing.T) {
	// BOLT #11 example invoice: created 1496314658, expiry 60 s
	got := Deadline("lnbc2500u1pvjluezpp5qqqsyqcyq5rqwzqfqqqsyqcyq5rqwzqfqqqsyqcyq5rqwzqfqypqdq5xysxxatsyp3k7enxv4jsxqzpuaztrnwngzn3kdzw5hydlzf03qdgm2hdq27cqv3agm2awhz5se903vruatfhq77w3ls4evs3ch9zw97j25emudupq63nyw24cg27h2rspfj9srp")
	if got.Unix() != 1496314658+60 {
		t.Errorf("deadline = %d", got.Unix())
	}
	if d := time.Until(Deadline("garbage")); d < 59*time.Minute || d > time.Hour {
		t.Errorf("fallback deadline %v, want about one hour", d)
	}
}
