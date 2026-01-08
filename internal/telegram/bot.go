package telegram

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LightningTipBot/LightningTipBot/internal/i18n"
	"github.com/LightningTipBot/LightningTipBot/internal/runtime/mutex"

	limiter "github.com/LightningTipBot/LightningTipBot/internal/rate"

	"github.com/eko/gocache/store"

	"github.com/LightningTipBot/LightningTipBot/internal"
	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/storage"
	gocache "github.com/patrickmn/go-cache"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/lightningtipbot/telebot.v3"
)

type TipBot struct {
	DB       *Databases
	Bunt     *storage.DB
	ShopBunt *storage.DB
	Telegram *tb.Bot
	Client   *lnbits.Client
	limiter  map[string]limiter.Limiter
	Cache
}
type Cache struct {
	*store.GoCacheStore
}

var (
	botWalletInitialisation     = sync.Once{}
	telegramHandlerRegistration = sync.Once{}
)

// NewBot migrates data and creates a new bot
func NewBot() TipBot {
	gocacheClient := gocache.New(5*time.Minute, 10*time.Minute)
	gocacheStore := store.NewGoCache(gocacheClient, nil)
	// create sqlite databases
	dbs := AutoMigration()
	limiter.Start()
	return TipBot{
		DB:       dbs,
		Client:   lnbits.NewClient(internal.Configuration.Lnbits.AdminKey, internal.Configuration.Lnbits.Url),
		Bunt:     createBunt(internal.Configuration.Database.BuntDbPath),
		ShopBunt: createBunt(internal.Configuration.Database.ShopBuntDbPath),
		Telegram: newTelegramBot(),
		Cache:    Cache{GoCacheStore: gocacheStore},
	}
}

// newTelegramBot will create a new Telegram bot.
func newTelegramBot() *tb.Bot {
	tgb, err := tb.NewBot(tb.Settings{
		Token:     internal.Configuration.Telegram.ApiKey,
		Poller:    &tb.LongPoller{Timeout: 60 * time.Second},
		ParseMode: tb.ModeMarkdown,
		Verbose:   false,
	})
	if err != nil {
		panic(err)
	}
	return tgb
}

// initBotWallet will create / initialize the bot wallet
// todo -- may want to derive user wallets from this specific bot wallet (master wallet), since lnbits usermanager extension is able to do that.
func (bot TipBot) initBotWallet() error {
	botWalletInitialisation.Do(func() {
		_, err := bot.initWallet(bot.Telegram.Me)
		if err != nil {
			log.Errorln(fmt.Sprintf("[initBotWallet] Could not initialize bot wallet: %s", err.Error()))
			return
		}
	})
	return nil
}

// GracefulShutdown will gracefully shutdown the bot
// It will wait for all mutex locks to unlock before shutdown.
func (bot *TipBot) GracefulShutdown() {
	t := time.NewTicker(time.Second * 10)
	log.Infof("[shutdown] Graceful shutdown (timeout=10s).")
	for {
		select {
		case <-t.C:
			// timer expired
			log.Infof("[shutdown] Graceful shutdown timeout reached. Forcing shutdown.")
			return
		default:
			// check if all mutex locks are unlocked
			if mutex.IsEmpty() {
				log.Infof("[shutdown] Graceful shutdown successful.")
				return
			}
		}
		time.Sleep(time.Second)
		log.Tracef("[shutdown] Trying graceful shutdown...")
	}
}

// Start will initialize the Telegram bot and lnbits.
func (bot *TipBot) Start() {
	log.Infof("[Telegram] Authorized on account @%s", bot.Telegram.Me.Username)
	// initialize the bot wallet
	err := bot.initBotWallet()
	if err != nil {
		log.Errorf("Could not initialize bot wallet: %s", err.Error())
	}

	// register telegram handlers
	bot.registerTelegramHandlers()

	// download bot avatar once
	bot.downloadMyProfilePicture()

	// edit worker collects messages to edit and
	// periodically edits them
	bot.startEditWorker()

	// register callbacks for invoices
	initInvoiceEventCallbacks(bot)

	// register callbacks for user state changes
	initializeStateCallbackMessage(bot)

	// start the telegram bot
	go bot.Telegram.Start()

	go bot.restartPersistedTickets()
	// gracefully shutdown
	exit := make(chan os.Signal, 1) // we need to reserve to buffer size 1, so the notifier are not blocked
	// we need to catch SIGTERM and SIGSTOP
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM, syscall.SIGSTOP)
	<-exit
	// gracefully shutdown
	bot.GracefulShutdown()
}

// registerTelegramHandlers registers all telegram handlers
func (bot *TipBot) registerTelegramHandlers() {
	telegramHandlerRegistration.Do(func() {
		// Register reacji tipping handlers
		bot.Telegram.Handle(tb.OnReaction, bot.OnReactionHandler)
		bot.Telegram.Handle("/setreacji", bot.handleSetReacji)
		bot.Telegram.Handle("/myreacjis", bot.handleMyReacjis)
		bot.Telegram.Handle("/delreacji", bot.handleDelReacji)

		// ... (other existing handlers would go here) ...
	})
}

// OnReactionHandler processes reactions to messages for reacji tipping
func (bot *TipBot) OnReactionHandler(c tb.Context) error {
	// Ignore reactions from the bot itself
	if c.Reaction().User.ID == bot.Telegram.Me.ID {
		return nil
	}

	tipperTgUser := c.Reaction().User
	originalMessage := c.Reaction().Message
	tippeeTgUser := originalMessage.Sender
	reactionEmoji := c.Reaction().Emoji

	// Prevent self-tipping
	if tipperTgUser.ID == tippeeTgUser.ID {
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_self_tip_error"))
		return nil
	}

	// Get LNbits users
	lnbitsTipper, err := GetUser(tipperTgUser, *bot)
	if err != nil {
		log.Errorf("OnReactionHandler: Could not get tipper user %s: %v", GetUserStr(tipperTgUser), err)
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_tipper_no_wallet"))
		return nil
	}
	lnbitsTippee, err := GetUser(tippeeTgUser, *bot)
	if err != nil {
		log.Errorf("OnReactionHandler: Could not get tippee user %s: %v", GetUserStr(tippeeTgUser), err)
		// More specific message if tippee doesn't have a wallet
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_tippee_no_wallet", map[string]interface{}{"Tippee": GetUserStrMd(tippeeTgUser)}))
		return nil
	}

	// Get configured tip amount from tipper's settings
	tipAmount, found := bot.getReacjiTipAmount(lnbitsTipper, reactionEmoji)
	if !found {
		// No tip configured for this emoji, do nothing or send a message
		log.Debugf("OnReactionHandler: No reacji tip configured for %s by %s", reactionEmoji, GetUserStr(tipperTgUser))
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_not_configured", map[string]interface{}{"Emoji": reactionEmoji}))
		return nil
	}

	// Check tipper's balance
	tipperBalance, err := bot.GetUserBalance(lnbitsTipper)
	if err != nil {
		log.Errorf("OnReactionHandler: Could not get tipper balance for %s: %v", GetUserStr(tipperTgUser), err)
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_tip_failed_balance_check"))
		return err
	}
	if tipperBalance < tipAmount {
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_tip_failed_insufficient_balance", map[string]interface{}{"Amount": tipAmount, "Balance": tipperBalance}))
		return fmt.Errorf("insufficient balance for reacji tip")
	}

	// Create invoice for tippee
	tippeeWallet := lnbitsTippee.Wallet
	invoiceParams := lnbits.InvoiceParams{
		Amount: tipAmount,
		Memo:   fmt.Sprintf("Reacji tip %s from %s", reactionEmoji, GetUserStr(tipperTgUser)),
		Out:    false, // This is an incoming invoice for the tippee
	}
	invoice, err := tippeeWallet.Invoice(invoiceParams, bot.Client)
	if err != nil {
		log.Errorf("OnReactionHandler: Could not create invoice for tippee %s: %v", GetUserStr(tippeeTgUser), err)
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_tip_failed_invoice_creation"))
		return err
	}

	// Pay invoice from tipper
	tipperWallet := lnbitsTipper.Wallet
	paymentParams := lnbits.PaymentParams{
		Bolt11: invoice.PaymentRequest,
		Out:    true, // This is an outgoing payment from the tipper
	}
	_, err = tipperWallet.Pay(paymentParams, bot.Client)
	if err != nil {
		log.Errorf("OnReactionHandler: Could not pay invoice from tipper %s: %v", GetUserStr(tipperTgUser), err)
		bot.Telegram.Send(tipperTgUser, i18n.Translate(tipperTgUser.LanguageCode, "reacji_tip_failed_payment"))
		return err
	}

	// Update balances in cache
	bot.Cache.Delete(fmt.Sprintf("%s_balance", lnbitsTipper.Name))
	bot.Cache.Delete(fmt.Sprintf("%s_balance", lnbitsTippee.Name))

	// Send confirmation messages
	tipperMsg := i18n.Translate(tipperTgUser.LanguageCode, "reacji_tip_success_tipper", map[string]interface{}{
		"Amount": tipAmount,
		"Emoji":  reactionEmoji,
		"Tippee": GetUserStrMd(tippeeTgUser),
	})
	tippeeMsg := i18n.Translate(tippeeTgUser.LanguageCode, "reacji_tip_success_tippee", map[string]interface{}{
		"Amount": tipAmount,
		"Emoji":  reactionEmoji,
		"Tipper": GetUserStrMd(tipperTgUser),
	})

	bot.Telegram.Send(tipperTgUser, tipperMsg)
	
	// If in a group chat, reply to the original message. Otherwise, send a private message.
	if originalMessage.Chat.Type != tb.ChatPrivate {
		bot.Telegram.Reply(originalMessage, tippeeMsg)
	} else {
		bot.Telegram.Send(tippeeTgUser, tippeeMsg) 
	}

	log.Infof("Reacji tip: %s tipped %d sats to %s with %s", GetUserStr(tipperTgUser), tipAmount, GetUserStr(tippeeTgUser), reactionEmoji)
	return nil
}

// handleSetReacji allows a user to configure a reacji tip amount
func (bot *TipBot) handleSetReacji(c tb.Context) error {
	user := c.Sender()
	lnbitsUser, err := GetUser(user, *bot)
	if err != nil {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "start_wallet_create"))
	}

	args := strings.Split(c.Message().Text, " ")
	if len(args) != 3 {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_set_syntax"))
	}

	emoji := args[1]
	amountStr := args[2]
	amount, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || amount <= 0 {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_set_invalid_amount"))
	}

	bot.updateReacjiSettings(lnbitsUser, emoji, amount)
	err = UpdateUserRecord(lnbitsUser, *bot) // Persist the updated settings using UpdateUserRecord
	if err != nil {
		log.Errorf("handleSetReacji: Failed to update user settings for %s: %v", GetUserStr(user), err)
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_set_save_error"))
	}

	return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_set_success", map[string]interface{}{
		"Emoji":  emoji,
		"Amount": amount,
	}))
}

// handleMyReacjis displays the user's current reacji tip configurations
func (bot *TipBot) handleMyReacjis(c tb.Context) error {
	user := c.Sender()
	lnbitsUser, err := GetUser(user, *bot)
	if err != nil {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "start_wallet_create"))
	}

	if lnbitsUser.Settings == nil || len(lnbitsUser.Settings.ReacjiTips) == 0 {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_my_none_configured"))
	}

	var sb strings.Builder
	sb.WriteString(i18n.Translate(user.LanguageCode, "reacji_my_header") + "\n")
	for _, rs := range lnbitsUser.Settings.ReacjiTips {
		sb.WriteString(fmt.Sprintf("%s: %d sats\n", rs.Emoji, rs.Amount))
	}

	return bot.Telegram.Send(user, sb.String())
}

// handleDelReacji removes a reacji tip configuration
func (bot *TipBot) handleDelReacji(c tb.Context) error {
	user := c.Sender()
	lnbitsUser, err := GetUser(user, *bot)
	if err != nil {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "start_wallet_create"))
	}

	args := strings.Split(c.Message().Text, " ")
	if len(args) != 2 {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_del_syntax"))
	}

	emoji := args[1]

	if !bot.deleteReacjiSettings(lnbitsUser, emoji) {
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_del_not_found", map[string]interface{}{"Emoji": emoji}))
	}

	err = UpdateUserRecord(lnbitsUser, *bot) // Persist the updated settings using UpdateUserRecord
	if err != nil {
		log.Errorf("handleDelReacji: Failed to update user settings for %s: %v", GetUserStr(user), err)
		return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_del_save_error"))
	}

	return bot.Telegram.Send(user, i18n.Translate(user.LanguageCode, "reacji_del_success", map[string]interface{}{"Emoji": emoji}))
}

// getReacjiTipAmount retrieves the configured tip amount for a given emoji
func (bot *TipBot) getReacjiTipAmount(user *lnbits.User, emoji string) (int64, bool) {
	if user.Settings == nil {
		return 0, false
	}
	for _, rs := range user.Settings.ReacjiTips {
		if rs.Emoji == emoji {
			return rs.Amount, true
		}
	}
	return 0, false
}

// updateReacjiSettings adds or updates a reacji setting for a user
func (bot *TipBot) updateReacjiSettings(user *lnbits.User, emoji string, amount int64) {
	if user.Settings == nil {
		user.Settings = &lnbits.Settings{ID: user.ID} // Initialize settings if nil
	}
	if user.Settings.ReacjiTips == nil {
		user.Settings.ReacjiTips = []lnbits.ReacjiSettings{}
	}

	found := false
	for i := range user.Settings.ReacjiTips {
		if user.Settings.ReacjiTips[i].Emoji == emoji {
			user.Settings.ReacjiTips[i].Amount = amount
			found = true
			break
		}
	}
	if !found {
		user.Settings.ReacjiTips = append(user.Settings.ReacjiTips, lnbits.ReacjiSettings{
			SettingsID: user.ID, // Link to parent settings
			Emoji:      emoji,
			Amount:     amount,
		})
	}
}

// deleteReacjiSettings removes a reacji setting for a user
func (bot *TipBot) deleteReacjiSettings(user *lnbits.User, emoji string) bool {
	if user.Settings == nil || user.Settings.ReacjiTips == nil {
		return false
	}

	originalLen := len(user.Settings.ReacjiTips)
	var newReacjiTips []lnbits.ReacjiSettings
	for _, rs := range user.Settings.ReacjiTips {
		if rs.Emoji != emoji {
			newReacjiTips = append(newReacjiTips, rs)
		}
	}
	user.Settings.ReacjiTips = newReacjiTips
	return len(user.Settings.ReacjiTips) < originalLen // True if an item was removed
}
