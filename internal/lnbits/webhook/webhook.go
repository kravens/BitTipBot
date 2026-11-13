package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/LightningTipBot/LightningTipBot/internal"
	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/telegram"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"net/http"

	"github.com/LightningTipBot/LightningTipBot/internal/storage"

	"github.com/gorilla/mux"
	"github.com/tidwall/buntdb"
	tb "gopkg.in/lightningtipbot/telebot.v3"

	"github.com/LightningTipBot/LightningTipBot/internal/i18n"
)

type Server struct {
	httpServer *http.Server
	bot        *tb.Bot
	c          *lnbits.Client
	database   *gorm.DB
	buntdb     *storage.DB
}

type Webhook struct {
	CheckingID    string      `json:"checking_id"`
	Status        string      `json:"status"`  // LNbits v1: "success" or "pending"
	Amount        int64       `json:"amount"`
	Fee           int64       `json:"fee"`
	Memo          string      `json:"memo"`
	Time          string      `json:"time"`
	Bolt11        string      `json:"bolt11"`
	Preimage      string      `json:"preimage"`
	PaymentHash   string      `json:"payment_hash"`
	Extra         interface{} `json:"extra"`
	WalletID      string      `json:"wallet_id"`
	Webhook       string      `json:"webhook"`
	WebhookStatus interface{} `json:"webhook_status"`
}

func NewServer(bot *telegram.TipBot) *Server {
	srv := &http.Server{
		Addr:         internal.Configuration.Lnbits.WebhookServerUrl.Host,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
	apiServer := &Server{
		c:          bot.Client,
		database:   bot.DB.Users,
		bot:        bot.Telegram,
		httpServer: srv,
		buntdb:     bot.Bunt,
	}
	apiServer.httpServer.Handler = apiServer.newRouter()
	go apiServer.httpServer.ListenAndServe()
	log.Infof("[Webhook] Server started at %s", internal.Configuration.Lnbits.WebhookServerUrl)
	return apiServer
}

// GetUserByWalletId looks up a user by their wallet ID.
// The Wallet is embedded in the User struct with GORM prefix "wallet_", so the
// column for Wallet.ID in the users table is "wallet_id".
func (w *Server) GetUserByWalletId(walletId string) (*lnbits.User, error) {
	user := &lnbits.User{}
	tx := w.database.Where("wallet_id = ?", walletId).First(user)
	if tx.Error != nil {
		return user, tx.Error
	}
	return user, nil
}

var paymentHashFormat = regexp.MustCompile("^[0-9a-f]{64}$")

// settledAmount returns the amount in msat that LNbits says the user's wallet
// received for paymentHash. It fails unless that payment is settled.
func (w *Server) settledAmount(user *lnbits.User, paymentHash string) (int64, error) {
	payment, err := w.c.Payment(user.Wallet, paymentHash)
	// LNbits sends a webhook only once, so a short LNbits hiccup must not
	// lose the payment event.
	for try := 0; err != nil && try < 2; try++ {
		time.Sleep(time.Second)
		payment, err = w.c.Payment(user.Wallet, paymentHash)
	}
	if err != nil {
		return 0, err
	}
	// LNbits only returns the details to the wallet that owns the payment. An
	// outgoing payment of the same wallet has a negative amount.
	if !payment.Paid || payment.Details.PaymentHash != paymentHash ||
		payment.Details.WalletID != user.Wallet.ID || payment.Details.Amount <= 0 {
		return 0, fmt.Errorf("not a settled incoming payment of wallet %s", user.Wallet.ID)
	}
	return payment.Details.Amount, nil
}

// markHandled records paymentHash and reports whether this is the first time.
func (w *Server) markHandled(paymentHash string) (first bool, err error) {
	err = w.buntdb.Update(func(tx *buntdb.Tx) error {
		_, replaced, err := tx.Set("webhook:"+paymentHash, "1", nil)
		first = !replaced
		return err
	})
	return first, err
}

func (w *Server) newRouter() *mux.Router {
	router := mux.NewRouter()
	router.HandleFunc("/", w.receive).Methods(http.MethodPost)
	return router
}

func (w *Server) receive(writer http.ResponseWriter, request *http.Request) {
	log.Infoln("[Webhook] Received request")

	// Read the raw body so we can log it for debugging and still decode it
	rawBody, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		log.Errorf("[Webhook] Error reading request body: %s", err.Error())
		writer.WriteHeader(400)
		return
	}
	log.Debugf("[Webhook] Raw payload: %s", string(rawBody))

	// LNbits v1 sends the payload as a double-encoded JSON string (the body
	// is a JSON string containing escaped JSON). Try normal decode first;
	// if that fails, unquote the string and decode the inner JSON.
	payload := rawBody
	webhookEvent := Webhook{}
	err = json.NewDecoder(bytes.NewReader(payload)).Decode(&webhookEvent)
	if err != nil {
		// Try unquoting: the body might be a JSON string wrapping the real object
		var inner string
		if unquoteErr := json.Unmarshal(rawBody, &inner); unquoteErr == nil {
			payload = []byte(inner)
			err = json.Unmarshal(payload, &webhookEvent)
		}
		if err != nil {
			log.Errorf("[Webhook] Error decoding request: %s", err.Error())
			log.Errorf("[Webhook] Body was: %s", string(rawBody))
			writer.WriteHeader(400)
			return
		}
	}

	log.Infof("[Webhook] Decoded: payment_hash=%s wallet_id=%s status=%s amount=%d",
		webhookEvent.PaymentHash, webhookEvent.WalletID, webhookEvent.Status, webhookEvent.Amount)

	// Ignore non-settled payments — LNbits v1 uses "status" field ("success" = settled)
	if webhookEvent.Status != "success" {
		log.Infof("[Webhook] Ignoring non-success payment (status=%s, payment_hash=%s)",
			webhookEvent.Status, webhookEvent.PaymentHash)
		writer.WriteHeader(200)
		return
	}

	// The hash becomes part of an LNbits URL and of the replay key, so only
	// the form LNbits uses is accepted.
	if !paymentHashFormat.MatchString(webhookEvent.PaymentHash) {
		log.Errorf("[Webhook] Rejecting webhook with malformed payment_hash")
		writer.WriteHeader(400)
		return
	}

	user, err := w.GetUserByWalletId(webhookEvent.WalletID)
	if err != nil {
		log.Errorf("[Webhook] Error getting user for wallet_id %s: %s", webhookEvent.WalletID, err.Error())
		writer.WriteHeader(400)
		return
	}

	// Anyone who can reach this port can send a webhook, so the body is only
	// a hint. Ask LNbits whether this wallet really received the payment.
	amountMsat, err := w.settledAmount(user, webhookEvent.PaymentHash)
	if err != nil {
		log.Errorf("[Webhook] Rejecting webhook for payment_hash %s: %s", webhookEvent.PaymentHash, err.Error())
		writer.WriteHeader(400)
		return
	}
	// Handle every payment once, so a webhook can't be replayed.
	first, err := w.markHandled(webhookEvent.PaymentHash)
	if err != nil {
		log.Errorf("[Webhook] Error storing payment_hash %s: %s", webhookEvent.PaymentHash, err.Error())
		writer.WriteHeader(500)
		return
	}
	if !first {
		log.Infof("[Webhook] Ignoring repeated webhook for payment_hash %s", webhookEvent.PaymentHash)
		writer.WriteHeader(200)
		return
	}

	log.Infoln(fmt.Sprintf("[⚡️ WebHook] User %s (%d) received invoice of %d sat.",
		telegram.GetUserStr(user.Telegram), user.Telegram.ID, amountMsat/1000))

	// Always respond 200 immediately so LNbits doesn't retry
	writer.WriteHeader(200)

	// Try to find and fire a registered invoice callback (e.g. from LNURL-pay or lightning address)
	txInvoiceEvent := &telegram.InvoiceEvent{Invoice: &telegram.Invoice{PaymentHash: webhookEvent.PaymentHash}}
	err = w.buntdb.Get(txInvoiceEvent)
	if err == nil {
		// An invoice event was registered for this payment hash
		c := telegram.InvoiceCallback[txInvoiceEvent.Callback]
		if c.Function != nil {
			// Validate the event type before dispatching
			if assertErr := telegram.AssertEventType(txInvoiceEvent, c.Type); assertErr != nil {
				log.Errorln(assertErr)
				// Fall through to send the generic notification below
			} else {
				go c.Function(txInvoiceEvent)
				return
			}
		}
		// Callback key was registered but no handler found — fall through to generic notification
		log.Warnf("[Webhook] No callback handler for invoice event callback=%d, payment_hash=%s",
			txInvoiceEvent.Callback, webhookEvent.PaymentHash)
	}

	// Fallback: send a plain "you received X sat" message to the user
	_, sendErr := w.bot.Send(
		user.Telegram,
		fmt.Sprintf(i18n.Translate(user.Telegram.LanguageCode, "invoiceReceivedMessage"),
			amountMsat/1000),
	)
	if sendErr != nil {
		log.Errorf("[Webhook] Failed to send notification to user %s: %s",
			telegram.GetUserStr(user.Telegram), sendErr.Error())
	}
}
