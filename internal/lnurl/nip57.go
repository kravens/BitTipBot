package lnurl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"

	"github.com/nbd-wtf/go-nostr"
)

type Tag []string

type Tags []Tag

type NostrEvent struct {
	ID        string    `json:"id"`
	PubKey    string    `json:"pubkey"`
	CreatedAt time.Time `json:"created_at"`
	Kind      int       `json:"kind"`
	Tags      Tags      `json:"tags"`
	Content   string    `json:"content"`
	Sig       string    `json:"sig"`
}

var hexPubkey = regexp.MustCompile(`^[0-9a-f]{64}$`)

// zapTags returns the tags named exactly name that carry a value. go-nostr's
// Tags.GetFirst matches the name as a prefix, so GetFirst("p") also returns
// a "preimage" or "proxy" tag.
func zapTags(tags nostr.Tags, name string) []nostr.Tag {
	var found []nostr.Tag
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			found = append(found, tag)
		}
	}
	return found
}

// checkZapRequest applies the NIP-57 checks on a zap request (kind 9734)
// before the bot signs a receipt for it: one p tag holding a pubkey, at most
// one e tag, and an amount tag, when present, equal to the invoice amount.
func checkZapRequest(ev nostr.Event, amountMsat int64) error {
	if ev.Kind != 9734 {
		return fmt.Errorf("zap request has kind %d, not 9734", ev.Kind)
	}
	p := zapTags(ev.Tags, "p")
	if len(p) != 1 || !hexPubkey.MatchString(p[0][1]) {
		return fmt.Errorf("zap request needs exactly one p tag with a pubkey")
	}
	if len(zapTags(ev.Tags, "e")) > 1 {
		return fmt.Errorf("zap request has more than one e tag")
	}
	if amount := zapTags(ev.Tags, "amount"); len(amount) > 0 && amount[0][1] != strconv.FormatInt(amountMsat, 10) {
		return fmt.Errorf("zap request amount does not match the invoice amount")
	}
	return nil
}

// checkZapRecipient makes sure the bot signs zap receipts only for the
// recipient of the payment. Otherwise anyone could pay their own lightning
// address with a zap request naming another bot user and get a receipt,
// signed by the bot, that clients count as a zap of that user's note.
// Recipients without a registered key keep working, but a key that another
// bot user registered cannot be named.
func (w Lnurl) checkZapRecipient(user *lnbits.User, p string) error {
	if user.Settings != nil && user.Settings.Nostr.PubKey != "" {
		if !strings.EqualFold(user.Settings.Nostr.PubKey, p) {
			return fmt.Errorf("zap request p tag %s is not the recipient's key", p)
		}
		return nil
	}
	var n int64
	tx := w.database.Model(&lnbits.Settings{}).Where("nostr_pub_key = ? COLLATE NOCASE AND id <> ?", p, user.ID).Count(&n)
	if tx.Error != nil {
		return tx.Error
	}
	if n > 0 {
		return fmt.Errorf("zap request p tag %s belongs to another user", p)
	}
	return nil
}

// DescriptionHash is the SHA256 hash of the metadata
func (w Lnurl) Nip57DescriptionHash(zapEventSerialized string) string {
	hash := sha256.Sum256([]byte(zapEventSerialized))
	hashString := hex.EncodeToString(hash[:])
	return hashString
}
