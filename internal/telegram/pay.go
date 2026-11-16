package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LightningTipBot/LightningTipBot/internal/telegram/intercept"

	"github.com/LightningTipBot/LightningTipBot/internal/errors"

	"github.com/LightningTipBot/LightningTipBot/internal/runtime/mutex"
	"github.com/LightningTipBot/LightningTipBot/internal/storage"

	"github.com/LightningTipBot/LightningTipBot/internal/i18n"
	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/runtime"

	"github.com/LightningTipBot/LightningTipBot/internal/str"
	lnurl "github.com/fiatjaf/go-lnurl"
	decodepay "github.com/fiatjaf/ln-decodepay"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/lightningtipbot/telebot.v3"
)

var (
	paymentConfirmationMenu = &tb.ReplyMarkup{ResizeKeyboard: true}
	btnCancelPay            = paymentConfirmationMenu.Data("🚫 Cancel", "cancel_pay")
	btnPay                  = paymentConfirmationMenu.Data("✅ Pay", "confirm_pay")
)

func helpPayInvoiceUsage(ctx context.Context, errormsg string) string {
	if len(errormsg) > 0 {
		return fmt.Sprintf(Translate(ctx, "payHelpText"), fmt.Sprintf("%s", errormsg))
	} else {
		return fmt.Sprintf(Translate(ctx, "payHelpText"), "")
	}
}

type PayData struct {
	*storage.Base
	From            *lnbits.User         `json:"from"`
	Invoice         string               `json:"invoice"`
	Hash            string               `json:"hash"`
	Proof           string               `json:"proof"`
	Memo            string               `json:"memo"`
	Message         string               `json:"message"`
	Amount          int64                `json:"amount"`
	LanguageCode    string               `json:"languagecode"`
	SuccessAction   *lnurl.SuccessAction `json:"successAction"`
	TelegramMessage *tb.Message          `json:"telegrammessage"`
}

// payHandler invoked on "/pay lnbc..." command
func (bot *TipBot) payHandler(ctx intercept.Context) (intercept.Context, error) {
	// check and print all commands
	bot.anyTextHandler(ctx)
	user := LoadUser(ctx)
	if user.Wallet.ID == "" {
		return ctx, errors.Create(errors.UserNoWalletError)
	}
	if len(strings.Split(ctx.Message().Text, " ")) < 2 {
		NewMessage(ctx.Message(), WithDuration(0, bot))
		bot.trySendMessage(ctx.Sender(), helpPayInvoiceUsage(ctx, ""))
		return ctx, errors.Create(errors.InvalidSyntaxError)
	}
	userStr := GetUserStr(ctx.Sender())
	paymentRequest, err := getArgumentFromCommand(ctx.Message().Text, 1)
	if err != nil {
		NewMessage(ctx.Message(), WithDuration(0, bot))
		bot.trySendMessage(ctx.Sender(), helpPayInvoiceUsage(ctx, Translate(ctx, "invalidInvoiceHelpMessage")))
		errmsg := fmt.Sprintf("[/pay] Error: Could not getArgumentFromCommand: %s", err.Error())
		log.Errorln(errmsg)
		return ctx, errors.New(errors.InvalidSyntaxError, err)
	}
	paymentRequest = strings.ToLower(paymentRequest)
	// get rid of the URI prefix
	paymentRequest = strings.TrimPrefix(paymentRequest, "lightning:")

	// decode invoice
	bolt11, err := decodepay.Decodepay(paymentRequest)
	if err != nil {
		bot.trySendMessage(ctx.Sender(), helpPayInvoiceUsage(ctx, Translate(ctx, "invalidInvoiceHelpMessage")))
		errmsg := fmt.Sprintf("[/pay] Error: Could not decode invoice: %s", err.Error())
		log.Errorln(errmsg)
		return ctx, errors.New(errors.InvalidSyntaxError, err)
	}
	amount := int64(bolt11.MSatoshi / 1000)

	if amount <= 0 {
		bot.trySendMessage(ctx.Sender(), Translate(ctx, "invoiceNoAmountMessage"))
		errmsg := fmt.Sprint("[/pay] Error: invoice without amount")
		log.Warnln(errmsg)
		return ctx, errors.Create(errors.InvalidAmountError)
	}

	// check user balance first
	balance, err := bot.GetUserBalance(user)
	if err != nil {
		NewMessage(ctx.Message(), WithDuration(0, bot))
		errmsg := fmt.Sprintf("[/pay] Error: Could not get user balance: %s", err.Error())
		log.Errorln(errmsg)
		bot.trySendMessage(ctx.Sender(), Translate(ctx, "errorTryLaterMessage"))
		return ctx, errors.New(errors.GetBalanceError, err)
	}

	if amount > balance {
		NewMessage(ctx.Message(), WithDuration(0, bot))
		bot.trySendMessage(ctx.Sender(), fmt.Sprintf(Translate(ctx, "insufficientFundsMessage"), balance, amount))
		return ctx, errors.Create(errors.InvalidSyntaxError)
	}
	// send warning that the invoice might fail due to missing fee reserve
	if float64(amount) > float64(balance)*0.98 {
		bot.trySendMessage(ctx.Sender(), Translate(ctx, "feeReserveMessage"))
	}

	confirmText := fmt.Sprintf(Translate(ctx, "confirmPayInvoiceMessage"), amount)
	if len(bolt11.Description) > 0 {
		confirmText = confirmText + fmt.Sprintf(Translate(ctx, "confirmPayAppendMemo"), str.MarkdownEscape(bolt11.Description))
	}

	log.Infof("[/pay] Invoice entered. User: %s, amount: %d sat.", userStr, amount)

	// object that holds all information about the send payment
	id := fmt.Sprintf("pay:%d-%d-%s", ctx.Sender().ID, amount, RandStringRunes(5))

	// // // create inline buttons
	payButton := paymentConfirmationMenu.Data(Translate(ctx, "payButtonMessage"), "confirm_pay", id)
	cancelButton := paymentConfirmationMenu.Data(Translate(ctx, "cancelButtonMessage"), "cancel_pay", id)

	paymentConfirmationMenu.Inline(
		paymentConfirmationMenu.Row(
			payButton,
			cancelButton),
	)
	payMessage := bot.trySendMessageEditable(ctx.Chat(), confirmText, paymentConfirmationMenu)
	// read successaction
	sa, ok := ctx.Value("SuccessAction").(*lnurl.SuccessAction)
	if !ok {
		sa = &lnurl.SuccessAction{}
	}

	payData := &PayData{
		Base:            storage.New(storage.ID(id)),
		From:            user,
		Invoice:         paymentRequest,
		Amount:          int64(amount),
		Memo:            bolt11.Description,
		Message:         confirmText,
		LanguageCode:    ctx.Value("publicLanguageCode").(string),
		SuccessAction:   sa,
		TelegramMessage: payMessage,
	}
	// add result to persistent struct
	runtime.IgnoreError(payData.Set(payData, bot.Bunt))

	SetUserState(user, bot, lnbits.UserStateConfirmPayment, paymentRequest)
	return ctx, nil
}

// confirmPayHandler when user clicked pay on payment confirmation
func (bot *TipBot) confirmPayHandler(ctx intercept.Context) (intercept.Context, error) {
	tx := &PayData{Base: storage.New(storage.ID(ctx.Data()))}
	mutex.LockWithContext(ctx, tx.ID)
	defer mutex.UnlockWithContext(ctx, tx.ID)
	sn, err := tx.Get(tx, bot.Bunt)
	// immediatelly set intransaction to block duplicate calls
	if err != nil {
		log.Errorf("[confirmPayHandler] %s", err.Error())
		return ctx, err
	}
	payData := sn.(*PayData)

	// onnly the correct user can press
	if payData.From.Telegram.ID != ctx.Sender().ID {
		return ctx, errors.Create(errors.UnknownError)
	}
	if !payData.Active {
		log.Errorf("[confirmPayHandler] send not active anymore")
		bot.tryEditMessage(ctx.Message(), i18n.Translate(payData.LanguageCode, "errorTryLaterMessage"), &tb.ReplyMarkup{})
		bot.tryDeleteMessage(ctx.Message())
		return ctx, errors.Create(errors.NotActiveError)
	}
	defer payData.Set(payData, bot.Bunt)

	// remove buttons from confirmation message
	// bot.tryEditMessage(handler.Message(), MarkdownEscape(payData.Message), &tb.ReplyMarkup{})

	user := LoadUser(ctx)
	if user.Wallet.ID == "" {
		bot.tryDeleteMessage(ctx.Message())
		return ctx, errors.Create(errors.UserNoWalletError)
	}

	// reset state immediately
	ResetUserState(user, bot)

	userStr := GetUserStr(ctx.Sender())

	// update button text
	bot.tryEditMessage(
		ctx.Message(),
		payData.Message,
		&tb.ReplyMarkup{
			InlineKeyboard: [][]tb.InlineButton{
				{tb.InlineButton{Unique: "attempt_payment", Text: i18n.Translate(payData.LanguageCode, "lnurlGettingUserMessage")}},
			},
		},
	)

	log.Infof("[/pay] Attempting %s's invoice %s (%d sat)", userStr, payData.ID, payData.Amount)
	// pay invoice
	invoice, err := user.Wallet.Pay(lnbits.PaymentParams{Out: true, Bolt11: payData.Invoice}, bot.Client)
	if err == lnbits.ErrPaymentPending {
		// neither paid nor failed yet: say so and report the result later
		log.Warnf("[/pay] Invoice %s of %s is still pending", payData.ID, userStr)
		payData.Hash = invoice.PaymentHash
		bot.tryEditMessage(ctx.Message(), i18n.Translate(payData.LanguageCode, "invoicePendingMessage"), &tb.ReplyMarkup{})
		go bot.reportPendingPayment(ctx.Message(), user, payData)
		return ctx, err
	}
	if err != nil {
		log.Errorf("[/pay] Could not pay invoice of %s: %s", userStr, err)
		return ctx, bot.payFailed(ctx.Message(), payData)
	}
	payData.Hash = invoice.PaymentHash
	bot.paySucceeded(ctx.Message(), user, payData)
	return ctx, nil
}

// payFailed tells the user that the payment of payData failed, in msg.
func (bot *TipBot) payFailed(msg *tb.Message, payData *PayData) error {
	err := fmt.Errorf(i18n.Translate(payData.LanguageCode, "invoiceUndefinedErrorMessage"))
	bot.tryEditMessage(msg, fmt.Sprintf(i18n.Translate(payData.LanguageCode, "invoicePaymentFailedMessage"), err.Error()), &tb.ReplyMarkup{})
	return err
}

// paySucceeded tells the user that the payment of payData was sent, replacing
// msg (the confirmation message), and shows the LNURL success action.
func (bot *TipBot) paySucceeded(msg *tb.Message, user *lnbits.User, payData *PayData) {
	userStr := GetUserStr(user.Telegram)
	// do balance check for keyboard update
	if _, err := bot.GetUserBalance(user); err != nil {
		log.Errorf("could not get balance of user %s", userStr)
	}

	if msg.Private() {
		// if the command was invoked in private chat
		// delete and send instead of edit, so the keyboard pops up again
		bot.tryDeleteMessage(msg)
		bot.trySendMessage(user.Telegram, i18n.Translate(payData.LanguageCode, "invoicePaidMessage"))
	} else {
		// if the command was invoked in group chat
		bot.trySendMessage(user.Telegram, i18n.Translate(payData.LanguageCode, "invoicePaidMessage"))
		bot.tryEditMessage(msg, fmt.Sprintf(i18n.Translate(payData.LanguageCode, "invoicePublicPaidMessage"), userStr), &tb.ReplyMarkup{})
	}

	// display LNURL success action if present
	// nil when the LNURL service sent no success action
	sa := payData.SuccessAction
	if sa == nil {
		sa = &lnurl.SuccessAction{}
	}
	if sa.Tag == "message" && len(sa.Message) > 0 {
		bot.trySendMessage(user.Telegram, fmt.Sprintf("✉️: `%s`", sa.Message))
	} else if sa.Tag == "url" && len(sa.URL) > 0 {
		bot.trySendMessage(user.Telegram, fmt.Sprintf("🔗: %s", str.MarkdownEscape(sa.URL)), tb.NoPreview)
		if len(sa.Description) > 0 {
			bot.trySendMessage(user.Telegram, fmt.Sprintf("✉️: %s", sa.Description))
		}
	}

	log.Infof("[⚡️ pay] User %s paid invoice %s (%d sat)", userStr, payData.ID, payData.Amount)
}

// cancelPaymentHandler invoked when user clicked cancel on payment confirmation
func (bot *TipBot) cancelPaymentHandler(ctx intercept.Context) (intercept.Context, error) {
	// reset state immediately
	user := LoadUser(ctx)
	ResetUserState(user, bot)
	tx := &PayData{Base: storage.New(storage.ID(ctx.Data()))}
	mutex.LockWithContext(ctx, tx.ID)
	defer mutex.UnlockWithContext(ctx, tx.ID)
	// immediatelly set intransaction to block duplicate calls
	sn, err := tx.Get(tx, bot.Bunt)
	if err != nil {
		log.Errorf("[cancelPaymentHandler] %s", err.Error())
		return ctx, err
	}
	payData := sn.(*PayData)
	// onnly the correct user can press
	if payData.From.Telegram.ID != ctx.Callback().Sender.ID {
		return ctx, errors.Create(errors.UnknownError)
	}
	// delete and send instead of edit for the keyboard to pop up after sending
	bot.tryDeleteMessage(ctx.Message())
	bot.trySendMessage(ctx.Message().Chat, i18n.Translate(payData.LanguageCode, "paymentCancelledMessage"))
	// bot.tryEditMessage(c.Message, i18n.Translate(payData.LanguageCode, "paymentCancelledMessage"), &tb.ReplyMarkup{})
	return ctx, payData.Inactivate(payData, bot.Bunt)

}

// reportPendingPayment waits for a payment that Pay left as pending and
// finishes msg (the confirmation message, now saying "in progress") the same
// way confirmPayHandler would have. The checks also make LNbits release the
// reserved amount as soon as the payment has failed.
// ponytail: in-memory only, a bot restart drops the watch; LNbits then settles
// the payment in its own background task and the user sees it in /transactions.
func (bot *TipBot) reportPendingPayment(msg *tb.Message, user *lnbits.User, payData *PayData) {
	err := bot.Client.AwaitPayment(user.Wallet, payData.Hash, 24*time.Hour, time.Minute)
	switch {
	case err == nil:
		bot.paySucceeded(msg, user, payData)
	case err == lnbits.ErrPaymentFailed:
		log.Errorf("[/pay] Pending invoice %s of %s failed", payData.ID, GetUserStr(user.Telegram))
		bot.payFailed(msg, payData)
	default:
		log.Warnf("[/pay] Payment %s of %s is still pending after a day", payData.Hash, GetUserStr(user.Telegram))
	}
}
