package lnbits

import (
	"errors"
	"fmt"
	"time"

	"github.com/LightningTipBot/LightningTipBot/internal/invoicepoll"
	"github.com/imroc/req"
)

// NewClient returns a new lnbits api client. Pass your API key and url here.
func NewClient(key, url string) *Client {
	return &Client{
		url: url,
		// info: this header holds the ADMIN key for the entire API
		// it can be used to create wallets for example
		// if you want to check the balance of a user, use w.Inkey
		// if you want to make a payment, use w.Adminkey
		header: req.Header{
			"Content-Type": "application/json",
			"Accept":       "application/json",
			"X-Api-Key":    key,
		},
	}
}

// CreateAccount creates a new LNbits account with a wallet using the native
// v1 API endpoint POST /api/v1/account. This replaces the deprecated User
// Manager extension. The response includes wallet keys directly.
func (c *Client) CreateAccount(walletName string) (wal Wallet, err error) {
	resp, err := req.Post(
		c.url+"/api/v1/account",
		c.header,
		req.BodyJSON(struct {
			Name string `json:"name"`
		}{Name: walletName}),
	)
	if err != nil {
		return
	}

	if resp.Response().StatusCode >= 300 {
		var reqErr Error
		resp.ToJSON(&reqErr)
		err = reqErr
		return
	}
	err = resp.ToJSON(&wal)
	return
}

// Invoice creates an invoice associated with this wallet.
func (w Wallet) Invoice(params InvoiceParams, c *Client) (Invoice, error) {
	return c.InvoiceForWallet(w.Inkey, params)
}

// InvoiceForWallet creates an invoice for a specific wallet using that
// wallet's inkey. The walletInkey authenticates the request so the invoice
// is created for (and credited to) the correct wallet.
func (c *Client) InvoiceForWallet(walletInkey string, params InvoiceParams) (Invoice, error) {
	params.Out = false
	var result Invoice
	walletHeader := req.Header{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-Api-Key":    walletInkey,
	}
	resp, err := req.Post(c.url+"/api/v1/payments", walletHeader, req.BodyJSON(&params))
	if err != nil {
		return result, err
	}
	if resp.Response().StatusCode >= 300 {
		var reqErr Error
		resp.ToJSON(&reqErr)
		return result, reqErr
	}
	err = resp.ToJSON(&result)
	if err == nil && params.Webhook != "" && invoicepoll.PaidHook != nil {
		// the webhook may never arrive, so also poll for the payment
		hash := result.PaymentHash
		watch := invoicepoll.Watch
		if params.Public {
			watch = invoicepoll.WatchPublic
		}
		go watch(hash, invoicepoll.Deadline(result.PaymentRequest), 2*time.Second, func() (invoicepoll.Status, error) {
			p, err := c.Payment(Wallet{Inkey: walletInkey}, hash)
			return invoicepoll.Status{
				Paid:       p.Paid && p.Details.PaymentHash == hash && p.Details.Amount > 0,
				WalletID:   p.Details.WalletID,
				AmountMsat: p.Details.Amount,
			}, err
		})
	}
	return result, err
}

// Info returns wallet information
func (c Client) Info(w Wallet) (wtx Wallet, err error) {
	// custom header with invoice key
	invoiceHeader := req.Header{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-Api-Key":    w.Inkey,
	}
	resp, err := req.Get(c.url+"/api/v1/wallet", invoiceHeader, nil)
	if err != nil {
		return
	}

	if resp.Response().StatusCode >= 300 {
		var reqErr Error
		resp.ToJSON(&reqErr)
		err = reqErr
		return
	}

	err = resp.ToJSON(&wtx)
	return
}

// Payments returns wallet payments
func (c Client) Payments(w Wallet) (wtx Payments, err error) {
	// custom header with invoice key
	invoiceHeader := req.Header{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-Api-Key":    w.Inkey,
	}
	resp, err := req.Get(c.url+"/api/v1/payments?limit=60", invoiceHeader, nil)
	if err != nil {
		return
	}

	if resp.Response().StatusCode >= 300 {
		var reqErr Error
		resp.ToJSON(&reqErr)
		err = reqErr
		return
	}

	err = resp.ToJSON(&wtx)
	return
}

// Payment state of a payment
func (c Client) Payment(w Wallet, payment_hash string) (payment LNbitsPayment, err error) {
	// custom header with invoice key
	invoiceHeader := req.Header{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-Api-Key":    w.Inkey,
	}
	resp, err := req.Get(c.url+fmt.Sprintf("/api/v1/payments/%s", payment_hash), invoiceHeader, nil)
	if err != nil {
		return
	}

	if resp.Response().StatusCode >= 300 {
		var reqErr Error
		resp.ToJSON(&reqErr)
		err = reqErr
		return
	}

	err = resp.ToJSON(&payment)
	return
}

// ErrPaymentPending means LNbits has not settled or failed the payment yet.
// The amount stays reserved in the wallet until it does.
var ErrPaymentPending = errors.New("payment is still pending")

// ErrPaymentFailed means the payment failed after LNbits first reported it as
// pending.
var ErrPaymentFailed = errors.New("payment failed")

// PayWait is how long Pay keeps asking LNbits about a payment it reported as
// pending. LNbits answers after 5 s by default while LND keeps trying routes
// for 30 s, so this has to outlast LND.
var PayWait = 45 * time.Second

// awaitPayment calls check every interval until the payment is paid (nil) or
// failed (ErrPaymentFailed). After timeout it returns ErrPaymentPending.
// Errors of check are retried: the payment may still go through.
func awaitPayment(check func() (LNbitsPayment, error), timeout, interval time.Duration) error {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		time.Sleep(interval)
		p, err := check()
		if err != nil {
			continue
		}
		if p.Paid {
			return nil
		}
		if p.Status == "failed" || p.Details.Status == "failed" {
			return ErrPaymentFailed
		}
	}
	return ErrPaymentPending
}

// AwaitPayment waits for an outgoing payment of w to settle, see awaitPayment.
// Every check also makes LNbits ask its funding source, which releases the
// reserved amount of a failed payment right away instead of at the next run
// of its background task.
func (c Client) AwaitPayment(w Wallet, paymentHash string, timeout, interval time.Duration) error {
	return awaitPayment(func() (LNbitsPayment, error) { return c.Payment(w, paymentHash) }, timeout, interval)
}

// Pay pays a given invoice with funds from the wallet. LNbits v1 replies
// "pending" when its funding source takes longer than a few seconds; Pay then
// waits up to PayWait for the result. It returns ErrPaymentFailed when the
// payment failed, and the invoice together with ErrPaymentPending when it is
// still undecided.
func (w Wallet) Pay(params PaymentParams, c *Client) (wtx Invoice, err error) {
	// custom header with admin key
	adminHeader := req.Header{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-Api-Key":    w.Adminkey,
	}
	r := req.New()
	r.SetTimeout(time.Hour * 24)
	resp, err := r.Post(c.url+"/api/v1/payments", adminHeader, req.BodyJSON(&params))
	if err != nil {
		return
	}

	if resp.Response().StatusCode >= 300 {
		var reqErr Error
		resp.ToJSON(&reqErr)
		err = reqErr
		return
	}

	err = resp.ToJSON(&wtx)
	if err == nil && wtx.Status == "pending" {
		err = c.AwaitPayment(w, wtx.PaymentHash, PayWait, 2*time.Second)
	}
	return
}
