package telegram

import (
	"testing"

	tb "gopkg.in/lightningtipbot/telebot.v3"
)

// trySend* returns a nil *tb.Message when Telegram refuses a send. Editing or
// deleting it later, often from a goroutine without recover, must not panic.
func TestNilMessageIsIgnored(t *testing.T) {
	var bot TipBot
	var msg *tb.Message
	bot.tryDeleteMessage(msg)
	if _, err := bot.tryEditMessage(msg, "x"); err == nil {
		t.Fatal("editing a nil message should return an error")
	}
}
