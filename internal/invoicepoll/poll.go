// Package invoicepoll polls LNbits for invoices whose webhook may never
// arrive, for example because LNbits >= 1.7 refuses webhook URLs on its own
// host. It has no dependency on the bot config, so it can be tested.
package invoicepoll

import (
	"time"

	decodepay "github.com/fiatjaf/ln-decodepay"
	log "github.com/sirupsen/logrus"
)

// PaidHook is called once with the wallet id, payment hash and amount in msat
// of a settled invoice. The bot sets it at startup; while it is nil nothing
// is polled.
var PaidHook func(walletID, paymentHash string, amountMsat int64)

// Status is the result of one check of a payment.
type Status struct {
	Paid       bool
	WalletID   string
	AmountMsat int64
}

// slots caps the number of running watches: anyone can request invoices
// through LNURL-pay, and every watch costs a goroutine and LNbits requests.
var slots = make(chan struct{}, 1000)

// Watch calls check until it reports the payment as settled or deadline
// passes, then calls PaidHook. The interval grows by half each round, up to
// 30 s.
// ponytail: in-memory only, a bot restart drops pending watches; persist the
// list in buntdb if that turns out to matter. Without a free slot the invoice
// is not watched and only the webhook can report it.
func Watch(paymentHash string, deadline time.Time, interval time.Duration, check func() (Status, error)) {
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		log.Warnf("[invoicepoll] %d watches running, not watching %s", cap(slots), paymentHash)
		return
	}
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		if interval < 30*time.Second {
			interval = interval * 3 / 2
		}
		status, err := check()
		if err != nil {
			log.Debugf("[invoicepoll] %s: %v", paymentHash, err)
			continue
		}
		if status.Paid {
			PaidHook(status.WalletID, paymentHash, status.AmountMsat)
			return
		}
	}
}

// Deadline is the expiry of the bolt11 invoice, or one hour from now when it
// can't be decoded.
func Deadline(bolt11 string) time.Time {
	if inv, err := decodepay.Decodepay(bolt11); err == nil {
		return time.Unix(int64(inv.CreatedAt+inv.Expiry), 0)
	}
	return time.Now().Add(time.Hour)
}
