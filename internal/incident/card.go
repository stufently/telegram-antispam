package incident

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// maxCardField bounds one attacker-controlled field on the admin card. A
// display name can be hundreds of runes of decoration; the card has to stay
// scannable, and the parts that matter (id, chat, action) must not be pushed
// off the top by someone's signature.
const maxCardField = 64

// maxCardNote bounds the free-text note (today: a wrapped Telegram error),
// and maxCard the whole card. Telegram's own limit is 4096, but a card that
// long is not read — and the fields that matter are at the top.
const (
	maxCardNote = 200
	maxCard     = 1024
)

// formatCard renders the text that goes with an incident's evidence in the
// admin chat.
//
// It exists because copyMessage — deliberately, that is what makes it a copy
// rather than a forward — strips the origin. Without this header a moderator
// sees a spam message and cannot tell which chat it was posted in, who posted
// it, or whether anything was done about it; the card was three words long
// ("blocklist") and every one of those questions went unanswered.
//
// The incident id leads because it is the handle for everything else: the
// admin buttons, the log line, and `tg-antispam decide <id>`.
//
// Every attacker-controlled field goes through sanitize: a display name is
// whatever the spammer typed, and a newline in it would otherwise let them
// forge card lines ("chat: <a trusted chat>").
func formatCard(id int64, inc domain.Incident, chatTitle, note string, willAct bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "#%d %s → %s", id, clip(sanitize(inc.Verdict.Reason), maxCardField), outcomeLabel(inc, willAct))
	if note != "" {
		// A wrapped Telegram error can be long; the card must stay a card.
		fmt.Fprintf(&b, "\n%s", clip(sanitize(note), maxCardNote))
	}

	b.WriteString("\nchat: ")
	if t := clip(sanitize(chatTitle), maxCardField); t != "" {
		fmt.Fprintf(&b, "%s (%d)", t, inc.ChatID)
	} else {
		// No title: the lookup failed, or the chat has none. The id alone is
		// worse to read but still identifies the chat, which is the point.
		fmt.Fprintf(&b, "%d", inc.ChatID)
	}
	// The message id is what turns "somewhere in this chat" into a link a
	// moderator can follow; for an album, the first part plus how many.
	if n := len(inc.MessageIDs); n > 0 {
		fmt.Fprintf(&b, " msg=%d", inc.MessageIDs[0])
		if n > 1 {
			fmt.Fprintf(&b, " (+%d album parts)", n-1)
		}
	}
	if inc.ThreadID != 0 {
		fmt.Fprintf(&b, " topic=%d", inc.ThreadID)
	}

	b.WriteString("\nfrom: ")
	b.WriteString(senderLine(inc.Sender))

	return clip(b.String(), maxCard)
}

// outcomeLabel says what is being done about the incident, and is careful not
// to claim more than that. The card is sent BEFORE the sanction — evidence
// first, then notify, then act — so at this point the action is a decision,
// not a result; the true outcome lands in the log line Enforce writes
// (outcome=succeeded|partial|failed).
//
// What the label must get right is the difference between "acting" and
// "acting on nothing": a dry-run chat, a review-only verdict and an incident
// whose evidence never copied all leave the author untouched, and a card
// naming the action alone would have a moderator believe someone is already
// muted when nobody is.
func outcomeLabel(inc domain.Incident, willAct bool) string {
	action := clip(sanitize(string(inc.Verdict.Action)), maxCardField)
	switch {
	case inc.Verdict.ReviewOnly:
		return fmt.Sprintf("review only: nothing applied (suggested %s)", action)
	case inc.DryRun:
		return fmt.Sprintf("dry-run: nothing applied (would be %s)", action)
	case !willAct:
		return fmt.Sprintf("nothing applied (would be %s)", action)
	default:
		return fmt.Sprintf("applying %s", action)
	}
}

// senderLine identifies the author. A message posted on behalf of a channel
// has no user at all — From is nil in that update — so printing id=0 with an
// empty name would describe nobody.
func senderLine(s domain.Sender) string {
	// SenderChatID, not "UserID == 0": a channel post also carries Telegram's
	// channel_bot pseudo-user in From, so keying on an absent user id would
	// print that placeholder and lose the id the sanction actually uses.
	if s.SenderChatID != 0 {
		return fmt.Sprintf("channel id=%d (%s)", s.SenderChatID, clip(sanitize(string(s.Kind)), maxCardField))
	}

	parts := []string{}
	if tag := clip(sanitize(s.Username), maxCardField); tag != "" {
		parts = append(parts, "@"+tag)
	}
	parts = append(parts, fmt.Sprintf("id=%d", s.UserID))
	if name := clip(sanitize(s.DisplayName), maxCardField); name != "" {
		parts = append(parts, `"`+name+`"`)
	}
	if s.Kind != domain.SenderUser {
		parts = append(parts, "("+clip(sanitize(string(s.Kind)), maxCardField)+")")
	}
	return strings.Join(parts, " ")
}

// clip shortens a field to n runes, marking that it was cut. It counts runes
// rather than bytes so a Cyrillic name is not halved compared to a Latin one.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
