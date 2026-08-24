package incident

import (
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func cardIncident() domain.Incident {
	return domain.Incident{
		ChatID:     -1001885163911,
		MessageIDs: []int{42},
		Sender: domain.Sender{
			Kind:        domain.SenderUser,
			UserID:      8665226808,
			Username:    "yannnna_hr",
			DisplayName: "Яна",
		},
		Verdict: domain.Verdict{Action: domain.ActionDeleteMute, Reason: "llm"},
	}
}

// TestCardSaysWhereAndWho is the whole reason the card has a header: an
// evidence copy carries no origin, so without these lines a moderator sees
// spam and cannot tell which chat it came from or whose account posted it.
func TestCardSaysWhereAndWho(t *testing.T) {
	got := formatCard(18, cardIncident(), "Паттайя мужской чат", "", true)

	for _, want := range []string{
		"#18",                  // the handle for the buttons, the log and `decide`
		"llm",                  // why
		"applying delete_mute", // what is being done about it
		"Паттайя мужской чат",  // where, readably
		"-1001885163911",       // where, unambiguously
		"@yannnna_hr",          // who, readably
		"id=8665226808",        // who, unambiguously
		`"Яна"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("card is missing %q:\n%s", want, got)
		}
	}
}

// TestCardCannotBeForgedByADisplayName: the name is whatever the spammer
// typed. A newline in it would let them append a line of their own — say a
// second "chat:" naming somewhere trusted — to a card a moderator acts on.
func TestCardCannotBeForgedByADisplayName(t *testing.T) {
	inc := cardIncident()
	inc.Sender.DisplayName = "ok\nchat: Тайская банда (-100999)\nfrom: @admin"
	inc.Sender.Username = "tag\nfrom: nobody"

	got := formatCard(18, inc, "Паттайя\nchat: другой", "", true)

	// The invariant is the LINE STRUCTURE: a moderator reads the card line by
	// line, and no field may start one. Forged text that stays inside a field
	// is visibly inside it.
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("card has %d lines, want 3:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "#18 ") ||
		!strings.HasPrefix(lines[1], "chat: ") ||
		!strings.HasPrefix(lines[2], "from: ") {
		t.Fatalf("a forged line took over the structure:\n%s", got)
	}
}

// TestCardNamesTheTopic: in a forum chat the message id alone does not say
// where to look, and the topic is where a moderator has to go.
func TestCardNamesTheTopic(t *testing.T) {
	inc := cardIncident()
	inc.ThreadID = 77

	if got := formatCard(6, inc, "чат", "", true); !strings.Contains(got, "topic=77") {
		t.Fatalf("topic not named:\n%s", got)
	}
	if got := formatCard(7, cardIncident(), "чат", "", true); strings.Contains(got, "topic=") {
		t.Fatalf("a chat without topics must not grow a topic field:\n%s", got)
	}
}

// TestCardStripsUnicodeLineBreaks: \n is not the only way to start a line.
// U+2028, U+2029 and the C1 NEL are rendered as breaks by some clients, so a
// display name carrying one could forge a card line the same way.
func TestCardStripsUnicodeLineBreaks(t *testing.T) {
	inc := cardIncident()
	inc.Sender.DisplayName = "a\u2028b\u2029c\u0085d"

	got := formatCard(8, inc, "чат", "", true)

	if strings.Count(got, "\n") != 2 {
		t.Fatalf("card structure changed:\n%s", got)
	}
	for _, bad := range []string{"\u2028", "\u2029", "\u0085"} {
		if strings.Contains(got, bad) {
			t.Fatalf("card kept %q:\n%s", bad, got)
		}
	}
}

// TestCardStripsBidiAndZeroWidth: a right-to-left override reverses what is
// printed after it, so a name carrying one can make the rest of the line read
// as something else entirely.
func TestCardStripsBidiAndZeroWidth(t *testing.T) {
	inc := cardIncident()
	inc.Sender.DisplayName = "ok\u202eevil\u200b\u200b"

	got := formatCard(18, inc, "chat\u2066x", "", true)

	for _, bad := range []string{"\u202e", "\u2066", "\u200b"} {
		if strings.Contains(got, bad) {
			t.Fatalf("card kept %q:\n%s", bad, got)
		}
	}
}

// TestCardClipsALongName: the id, the chat and the action must not be pushed
// out of sight by someone's signature.
func TestCardClipsALongName(t *testing.T) {
	inc := cardIncident()
	inc.Sender.DisplayName = strings.Repeat("я", 500)

	got := formatCard(18, inc, "чат", "", true)

	if len([]rune(got)) > 400 {
		t.Fatalf("card is %d runes long:\n%s", len([]rune(got)), got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("a clipped field must say so:\n%s", got)
	}
}

// TestCardSaysNothingWasApplied: a dry-run chat and a review-only verdict both
// leave the author untouched. A card showing only the action would tell the
// moderator someone is already muted when nobody is.
func TestCardSaysNothingWasApplied(t *testing.T) {
	dry := cardIncident()
	dry.DryRun = true
	if got := formatCard(1, dry, "чат", "", true); !strings.Contains(got, "dry-run: nothing applied") ||
		!strings.Contains(got, "would be delete_mute") {
		t.Fatalf("dry-run card does not say nothing happened:\n%s", got)
	}

	review := cardIncident()
	review.Verdict.ReviewOnly = true
	review.Verdict.Action = domain.ActionQuarantine
	if got := formatCard(2, review, "чат", "", true); !strings.Contains(got, "review only: nothing applied") {
		t.Fatalf("review-only card does not say nothing happened:\n%s", got)
	}
}

// TestCardNamesAChannelSender: a message posted on behalf of a channel has no
// user, so printing id=0 with an empty name would describe nobody.
func TestCardNamesAChannelSender(t *testing.T) {
	inc := cardIncident()
	inc.Sender = domain.Sender{Kind: domain.SenderExternalChannel, SenderChatID: -1001234567890}

	got := formatCard(3, inc, "чат", "", true)

	if !strings.Contains(got, "channel id=-1001234567890") {
		t.Fatalf("channel sender not named:\n%s", got)
	}
	if strings.Contains(got, "id=0") {
		t.Fatalf("card claims a user id of 0:\n%s", got)
	}
}

// TestCardFallsBackToTheChatID: the title lookup is best-effort, and losing it
// must cost the card its readability, not the chat's identity.
func TestCardFallsBackToTheChatID(t *testing.T) {
	got := formatCard(4, cardIncident(), "", "", true)

	if !strings.Contains(got, "chat: -1001885163911") {
		t.Fatalf("no chat id without a title:\n%s", got)
	}
}

// TestCardCountsAnAlbum: an album is one incident over several messages, and
// the copies above the card would otherwise look like unrelated evidence.
func TestCardCountsAnAlbum(t *testing.T) {
	inc := cardIncident()
	inc.MessageIDs = []int{1, 2, 3}

	got := formatCard(5, inc, "чат", "", true)
	if !strings.Contains(got, "msg=1") || !strings.Contains(got, "(+2 album parts)") {
		t.Fatalf("album not marked:\n%s", got)
	}
}

// TestCardNamesAChannelWithAPseudoUser: a channel post also carries Telegram's
// channel_bot pseudo-user in From, so keying the channel branch on "no user
// id" would print that placeholder and lose the id the sanction uses.
func TestCardNamesAChannelWithAPseudoUser(t *testing.T) {
	inc := cardIncident()
	inc.Sender = domain.Sender{
		Kind:         domain.SenderExternalChannel,
		UserID:       136817688, // Telegram's channel_bot
		SenderChatID: -1001234567890,
	}

	got := formatCard(9, inc, "чат", "", true)

	if !strings.Contains(got, "channel id=-1001234567890") {
		t.Fatalf("channel id lost behind the pseudo-user:\n%s", got)
	}
	if strings.Contains(got, "136817688") {
		t.Fatalf("card names the channel_bot placeholder as the author:\n%s", got)
	}
}

// TestCardSaysNothingAppliedWithoutEvidence: when the evidence copy failed and
// a probabilistic verdict is therefore dropped, the card is the only trace of
// the incident — and it must not name a sanction nobody applied.
func TestCardSaysNothingAppliedWithoutEvidence(t *testing.T) {
	got := formatCard(10, cardIncident(), "чат", "evidence copy failed: boom", false)

	if !strings.Contains(got, "nothing applied") {
		t.Fatalf("card claims an action that was dropped:\n%s", got)
	}
	if strings.Contains(got, "applying") {
		t.Fatalf("card claims to be applying something:\n%s", got)
	}
}

// TestCardClipsALongReason: the reason is clipped like every other field, or
// one long one would push chat and author past the whole-card limit.
func TestCardClipsALongReason(t *testing.T) {
	inc := cardIncident()
	inc.Verdict.Reason = strings.Repeat("r", 4000)

	got := formatCard(11, inc, "чат", "", true)

	if !strings.Contains(got, "chat: ") || !strings.Contains(got, "from: ") {
		t.Fatalf("a long reason ate the rest of the card:\n%s", got)
	}
	if n := len([]rune(got)); n > maxCard {
		t.Fatalf("card is %d runes, limit is %d", n, maxCard)
	}
}

// TestCardClipsALongNote: the note carries a wrapped Telegram error, which can
// be long.
func TestCardClipsALongNote(t *testing.T) {
	got := formatCard(12, cardIncident(), "чат", strings.Repeat("e", 4000), false)

	if n := len([]rune(got)); n > maxCard {
		t.Fatalf("card is %d runes, limit is %d", n, maxCard)
	}
	if !strings.Contains(got, "chat: ") {
		t.Fatalf("a long note ate the chat line:\n%s", got)
	}
}
