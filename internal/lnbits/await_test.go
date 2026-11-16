package lnbits

import (
	"errors"
	"testing"
	"time"
)

func TestAwaitPayment(t *testing.T) {
	pending := LNbitsPayment{Status: "pending"}
	failedInDetails := LNbitsPayment{}
	failedInDetails.Details.Status = "failed"
	for name, c := range map[string]struct {
		replies []LNbitsPayment
		want    error
	}{
		"paid":              {[]LNbitsPayment{pending, {Paid: true, Status: "success"}}, nil},
		"failed":            {[]LNbitsPayment{pending, {Status: "failed"}}, ErrPaymentFailed},
		"failed in details": {[]LNbitsPayment{failedInDetails}, ErrPaymentFailed},
		"still pending":     {[]LNbitsPayment{pending}, ErrPaymentPending},
	} {
		calls := 0
		check := func() (LNbitsPayment, error) {
			calls++
			if calls == 1 {
				return LNbitsPayment{}, errors.New("LNbits unreachable") // retried
			}
			i := calls - 2
			if i >= len(c.replies) {
				i = len(c.replies) - 1
			}
			return c.replies[i], nil
		}
		if got := awaitPayment(check, 50*time.Millisecond, time.Millisecond); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}
