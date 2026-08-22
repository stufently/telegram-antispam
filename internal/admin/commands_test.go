package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

const (
	testChat  = int64(-100777)
	testAdmin = int64(50)
	testBotID = int64(999)
)

// cmdFixture wires a Commands over a fake port with recording hooks, which
// is all any of these tests need: the point of every case below is WHICH
// side effects run, not what Telegram does with them.
type cmdFixture struct {
	f         *fake.Fake
	c         *Commands
	reported  []domain.Incident
	fresh     bool
	reportErr error
	trained   []string // "from->to" per call
}

func newCmdFixture(t *testing.T) *cmdFixture {
	t.Helper()
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: testAdmin}}
	h := NewHandler(f, nil, nil)
	fx := &cmdFixture{f: f, fresh: true}
	fx.c = NewCommands(h, "antispambot", testBotID)
	fx.c.SetReporter(func(_ context.Context, inc domain.Incident) (bool, error) {
		fx.reported = append(fx.reported, inc)
		return fx.fresh, fx.reportErr
	})
	fx.c.SetRelabeler(func(_ int64, from, to string, _ []string) error {
		fx.trained = append(fx.trained, from+"->"+to)
		return nil
	})
	return fx
}

func spamCmd(from int64, reply *domain.Message) domain.Message {
	return domain.Message{
		ChatID:    testChat,
		MessageID: 10,
		Text:      "/spam",
		Sender:    domain.Sender{UserID: from, Kind: domain.SenderUser},
		ReplyTo:   reply,
	}
}

func target(userID int64) *domain.Message {
	return &domain.Message{
		ChatID:    testChat,
		MessageID: 7,
		Text:      "работа для всех желающих, пишите в лс",
		Sender:    domain.Sender{UserID: userID, Kind: domain.SenderUser},
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		text string
		want Command
		ok   bool
	}{
		{"/spam", CmdSpam, true},
		{"/ham", CmdHam, true},
		{"/SPAM", CmdSpam, true},
		{"/spam@antispambot", CmdSpam, true},
		{"/spam@AntiSpamBot вдогонку", CmdSpam, true},
		{"/spam@otherbot", "", false},
		{"посмотри /spam тут", "", false},
		{"/spammer", "", false},
		{"/start", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseCommand(tc.text, "antispambot")
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseCommand(%q) = (%q,%v), want (%q,%v)", tc.text, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCommandDeniedForNonAdmin(t *testing.T) {
	fx := newCmdFixture(t)
	fx.c.Handle(context.Background(), spamCmd(12345, target(42)))

	if len(fx.reported) != 0 || len(fx.trained) != 0 {
		t.Fatalf("stranger must cause no incident and no training, got %v / %v", fx.reported, fx.trained)
	}
	if fx.f.LastDelete.Chat != 0 {
		t.Fatal("stranger's command message must not be deleted")
	}
	if fx.f.LastEphemeral.Text == "" {
		t.Fatal("refusal must be reported back to the sender")
	}
}

// A failing admin lookup must deny, not fall through: /spam deletes and
// mutes, so "we could not check" has to mean "no".
func TestCommandFailsClosedOnAdminLookupError(t *testing.T) {
	fx := newCmdFixture(t)
	fx.f.AdminsErr = errors.New("telegram unreachable")

	fx.c.Handle(context.Background(), spamCmd(testAdmin, target(42)))

	if len(fx.reported) != 0 || len(fx.trained) != 0 {
		t.Fatal("auth error must produce no side effects")
	}
}

func TestCommandSpamHappyPath(t *testing.T) {
	fx := newCmdFixture(t)
	fx.c.Handle(context.Background(), spamCmd(testAdmin, target(42)))

	if len(fx.reported) != 1 {
		t.Fatalf("expected exactly one incident, got %d", len(fx.reported))
	}
	inc := fx.reported[0]
	if inc.Verdict.Action != domain.ActionDeleteMute {
		t.Errorf("action = %v, want delete_mute", inc.Verdict.Action)
	}
	if inc.Verdict.Reason != "manual_spam" {
		t.Errorf("reason = %q, want manual_spam", inc.Verdict.Reason)
	}
	if inc.DryRun {
		t.Error("a human report must act even in a dry-run chat")
	}
	if len(inc.Tokens) == 0 {
		t.Error("tokens must travel with the incident, or the buttons cannot train")
	}
	if len(inc.MessageIDs) != 1 || inc.MessageIDs[0] != 7 {
		t.Errorf("message ids = %v, want [7]", inc.MessageIDs)
	}
	if len(fx.trained) != 1 || fx.trained[0] != "ham->spam" {
		t.Errorf("training = %v, want one ham->spam", fx.trained)
	}
	if fx.f.LastDelete.Chat != testChat || len(fx.f.LastDelete.IDs) != 1 || fx.f.LastDelete.IDs[0] != 10 {
		t.Errorf("command message must be deleted, got %+v", fx.f.LastDelete)
	}
}

// The detector may have already acted on the same message. The sanction must
// not be repeated, but the moderator's label is still new information.
func TestCommandSpamDuplicateStillTrains(t *testing.T) {
	fx := newCmdFixture(t)
	fx.fresh = false

	fx.c.Handle(context.Background(), spamCmd(testAdmin, target(42)))

	if len(fx.trained) != 1 {
		t.Fatalf("duplicate report must still train, got %v", fx.trained)
	}
	if fx.f.LastEphemeral.Text == "" {
		t.Fatal("moderator must be told the message was already handled")
	}
}

func TestCommandSpamRequiresReply(t *testing.T) {
	fx := newCmdFixture(t)
	fx.c.Handle(context.Background(), spamCmd(testAdmin, nil))

	if len(fx.reported) != 0 {
		t.Fatal("a command without a reply must do nothing")
	}
	if fx.f.LastEphemeral.Text == "" {
		t.Fatal("missing reply must be explained to the moderator")
	}
}

func TestCommandRefusesAdminTarget(t *testing.T) {
	fx := newCmdFixture(t)
	// testAdmin is an administrator of this chat.
	fx.c.Handle(context.Background(), spamCmd(testAdmin, target(testAdmin)))

	if len(fx.reported) != 0 || len(fx.trained) != 0 {
		t.Fatal("an administrator's message must never be sanctioned by command")
	}
}

func TestCommandRefusesBotAndServiceTargets(t *testing.T) {
	fx := newCmdFixture(t)
	fx.c.Handle(context.Background(), spamCmd(testAdmin, target(testBotID)))
	if len(fx.reported) != 0 {
		t.Fatal("the bot's own message must be refused")
	}

	service := &domain.Message{ChatID: testChat, MessageID: 8}
	fx.c.Handle(context.Background(), spamCmd(testAdmin, service))
	if len(fx.reported) != 0 {
		t.Fatal("a message without an author must be refused")
	}
}

// Anonymous administrators post as the chat itself. They are the chat's
// owners often enough that refusing them would make the commands useless in
// exactly the chats that need them.
func TestCommandAllowsAnonymousAdmin(t *testing.T) {
	fx := newCmdFixture(t)
	m := spamCmd(0, target(42))
	m.Sender = domain.Sender{SenderChatID: testChat, Kind: domain.SenderAnonAdmin}

	fx.c.Handle(context.Background(), m)

	if len(fx.reported) != 1 {
		t.Fatalf("anonymous admin must be able to report spam, got %d incidents", len(fx.reported))
	}
}

func TestCommandHamLiftsAndRelabels(t *testing.T) {
	fx := newCmdFixture(t)
	m := spamCmd(testAdmin, target(42))
	m.Text = "/ham"

	fx.c.Handle(context.Background(), m)

	if fx.f.LastUnrestrict.UserID != 42 {
		t.Errorf("mute must be lifted for the target's author, got %+v", fx.f.LastUnrestrict)
	}
	if fx.f.LastUnban.UserID != 42 {
		t.Errorf("ban must be lifted too, got %+v", fx.f.LastUnban)
	}
	if len(fx.trained) != 1 || fx.trained[0] != "spam->ham" {
		t.Errorf("training = %v, want one spam->ham", fx.trained)
	}
	if len(fx.reported) != 0 {
		t.Error("/ham must not create an incident")
	}
}

func TestCommandMatch(t *testing.T) {
	fx := newCmdFixture(t)
	if !fx.c.Match(domain.Message{Text: "/spam"}) {
		t.Error("/spam must match")
	}
	if fx.c.Match(domain.Message{Text: "обычное сообщение"}) {
		t.Error("ordinary text must not match")
	}
}
