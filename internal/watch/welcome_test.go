package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

var _ WelcomeStore = (*store.DB)(nil)

type memWelcome struct {
	welcomed map[[2]int64]bool
	trust    map[[2]int64]int
	chats    map[int64]store.ChatRow
	found    map[int64]bool
	marks    int
	errWas   error
	errMark  error
	errTrust error
	errChat  error
}

func (m *memWelcome) WasWelcomed(chatID, userID int64) (bool, error) {
	if m.errWas != nil {
		return false, m.errWas
	}
	return m.welcomed[[2]int64{chatID, userID}], nil
}

func (m *memWelcome) MarkWelcomed(chatID, userID int64) error {
	if m.errMark != nil {
		return m.errMark
	}
	if m.welcomed == nil {
		m.welcomed = map[[2]int64]bool{}
	}
	m.welcomed[[2]int64{chatID, userID}] = true
	m.marks++
	return nil
}

func (m *memWelcome) TrustCount(chatID, userID int64) (int, error) {
	if m.errTrust != nil {
		return 0, m.errTrust
	}
	return m.trust[[2]int64{chatID, userID}], nil
}

func (m *memWelcome) GetChat(chatID int64) (store.ChatRow, bool, error) {
	if m.errChat != nil {
		return store.ChatRow{}, false, m.errChat
	}
	row, ok := m.chats[chatID]
	if m.found != nil {
		ok = m.found[chatID]
	}
	return row, ok, nil
}

type listedIDs map[int64]bool

func (l listedIDs) Listed(id int64) bool { return l[id] }

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func welcomeCfg(mode string, allow []int64, w config.Welcome) *config.Config {
	if w.MaxPerMinute == nil {
		w.MaxPerMinute = intPtr(20)
	}
	return &config.Config{Chats: config.ChatsPolicy{Mode: mode, Allowlist: allow}, Welcome: w}
}

func onWelcome(text string) config.Welcome {
	return config.Welcome{Enabled: boolPtr(true), Text: text}
}

func disabledRow() *memWelcome {
	return &memWelcome{
		chats: map[int64]store.ChatRow{-100: {Enabled: false}},
		found: map[int64]bool{-100: true},
	}
}

func sends(calls []string) int {
	n := 0
	for _, c := range calls {
		if c == "SendWelcome" {
			n++
		}
	}
	return n
}

func newW(cfg *config.Config, st *memWelcome, port *fake.Fake, list listedIDs) *Welcomer {
	return &Welcomer{Config: config.NewStore(cfg), Store: st, Port: port, Blocklist: list}
}

func TestWelcomerSendsOncePerChatUser(t *testing.T) {
	st := &memWelcome{
		chats: map[int64]store.ChatRow{
			-100: {ChatID: -100, Enabled: true, DryRun: true},
			-200: {ChatID: -200, Enabled: true, DryRun: true},
		},
		found: map[int64]bool{-100: true, -200: true},
	}
	port := fake.New()
	w := newW(welcomeCfg("auto", nil, config.Welcome{
		Enabled: boolPtr(false),
		Text:    "global text that must not be sent",
		Chats: map[int64]config.WelcomeChat{
			-100: {Enabled: boolPtr(true), Text: "  rules for A. mistaken mute: @desk  "},
			-200: {Enabled: boolPtr(true), Text: "rules for B"},
		},
	}), st, port, listedIDs{})
	w.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	check := func(chat, user int64, want Outcome) {
		t.Helper()
		out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: chat, UserID: user})
		if err != nil || out != want {
			t.Fatalf("chat %d user %d = %q err=%v, want %q", chat, user, out, err, want)
		}
	}
	check(-100, 7, OutcomeSent)
	if port.LastWelcome.Text != "rules for A. mistaken mute: @desk" || port.LastWelcome.Chat != -100 || port.LastWelcome.UserID != 7 {
		t.Fatalf("sent %+v", port.LastWelcome)
	}
	check(-100, 7, OutcomeSkipKnown)
	check(-100, 8, OutcomeSent)
	check(-200, 7, OutcomeSent)
	if port.LastWelcome.Chat != -200 || port.LastWelcome.Text != "rules for B" {
		t.Fatalf("last send %+v", port.LastWelcome)
	}
	if got, marks := sends(port.Calls()), st.marks; got != 3 || marks != 3 {
		t.Fatalf("sends=%d marks=%d, want 3 and 3", got, marks)
	}
}

func TestWelcomerSkips(t *testing.T) {
	enabled := onWelcome("hello")
	cases := []struct {
		name  string
		cfg   *config.Config
		store *memWelcome
		list  listedIDs
		want  Outcome
	}{
		{"not admitted", welcomeCfg("allowlist", []int64{-1}, enabled), &memWelcome{}, nil, OutcomeSkipNotAdmitted},
		{"disabled", welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(false), Text: "hello"}), &memWelcome{}, nil, OutcomeSkipDisabled},
		{"chat row disabled", welcomeCfg("auto", nil, enabled), disabledRow(), nil, OutcomeSkipChatDisabled},
		{"blocklisted", welcomeCfg("auto", nil, enabled), &memWelcome{}, listedIDs{7: true}, OutcomeSkipBlocklisted},
		{"already welcomed", welcomeCfg("auto", nil, enabled), &memWelcome{welcomed: map[[2]int64]bool{{-100, 7}: true}}, nil, OutcomeSkipKnown},
		{"already trusted", welcomeCfg("auto", nil, enabled), &memWelcome{trust: map[[2]int64]int{{-100, 7}: 1}}, nil, OutcomeSkipKnown},
		{"blocklist before known", welcomeCfg("auto", nil, enabled), &memWelcome{welcomed: map[[2]int64]bool{{-100, 7}: true}}, listedIDs{7: true}, OutcomeSkipBlocklisted},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			port := fake.New()
			out, err := newW(tt.cfg, tt.store, port, tt.list).Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
			if err != nil || out != tt.want || sends(port.Calls()) != 0 {
				t.Fatalf("got %q err=%v sends=%d, want %q and no send", out, err, sends(port.Calls()), tt.want)
			}
		})
	}
}

func TestWelcomerRateCap(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := &memWelcome{}
	port := fake.New()
	w := newW(welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(2)}), st, port, nil)
	w.Now = func() time.Time { return now }
	for i, user := range []int64{1, 2, 3} {
		out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: user})
		want := OutcomeSent
		if i == 2 {
			want = OutcomeSkipRateCapped
		}
		if err != nil || out != want {
			t.Fatalf("user %d = %q err=%v, want %q", user, out, err, want)
		}
	}
	if sends(port.Calls()) != 2 || st.marks != 2 {
		t.Fatalf("sends=%d marks=%d, want 2 and 2", sends(port.Calls()), st.marks)
	}
	now = now.Add(61 * time.Second)
	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 4})
	if err != nil || out != OutcomeSent || sends(port.Calls()) != 3 {
		t.Fatalf("after 61s = %q err=%v sends=%d", out, err, sends(port.Calls()))
	}

	// A send that outlives the minute is stamped when it finishes, so the
	// next joiner still sees it inside the window.
	base := time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)
	var ticks int
	late := fake.New()
	slow := newW(welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(1)}), &memWelcome{}, late, nil)
	slow.Now = func() time.Time {
		ticks++
		if ticks == 1 {
			return base
		}
		return base.Add(61 * time.Second)
	}
	out, err = slow.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeSent {
		t.Fatalf("slow send = %q err=%v", out, err)
	}
	out, err = slow.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 2})
	if err != nil || out != OutcomeSkipRateCapped || sends(late.Calls()) != 1 {
		t.Fatalf("after a slow send = %q err=%v sends=%d", out, err, sends(late.Calls()))
	}
}

func TestWelcomerSendFailureNotMarked(t *testing.T) {
	st := &memWelcome{}
	port := fake.New()
	port.WelcomeErr = errors.New("telegram down")
	w := newW(welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(1)}), st, port, nil)
	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
	known, kerr := st.WasWelcomed(-100, 7)
	if out != OutcomeError || !errors.Is(err, port.WelcomeErr) || st.marks != 0 || kerr != nil || known {
		t.Fatalf("out=%q err=%v marks=%d known=%v kerr=%v", out, err, st.marks, known, kerr)
	}
	port.WelcomeErr = nil
	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 8})
	if err != nil || out != OutcomeSkipRateCapped || sends(port.Calls()) != 1 {
		t.Fatalf("failed attempt did not consume the cap: %q err=%v sends=%d", out, err, sends(port.Calls()))
	}
}

func TestWelcomerAdmitReservesInFlight(t *testing.T) {
	port := fake.New()
	w := newW(welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(1)}), &memWelcome{}, port, nil)
	out, ticket, err := w.Admit(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeQueued || ticket == nil {
		t.Fatalf("first=%q err=%v ticket=%v", out, err, ticket)
	}
	out, ticket, err = w.Admit(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 2})
	if err != nil || out != OutcomeSkipRateCapped || ticket != nil {
		t.Fatalf("other=%q err=%v ticket=%v", out, err, ticket)
	}
	out, ticket, err = w.Admit(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeSkipKnown || ticket != nil {
		t.Fatalf("same=%q err=%v ticket=%v", out, err, ticket)
	}
	if sends(port.Calls()) != 0 {
		t.Fatal("a reserved ticket must not send")
	}
}

func TestWelcomerStoreErrorFailsClosed(t *testing.T) {
	boom := errors.New("db")
	for _, tt := range []struct {
		name  string
		store *memWelcome
	}{
		{"get chat", &memWelcome{errChat: boom}},
		{"was welcomed", &memWelcome{errWas: boom}},
		{"trust count", &memWelcome{errTrust: boom}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port := fake.New()
			out, err := newW(welcomeCfg("auto", nil, onWelcome("hello")), tt.store, port, nil).
				Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
			if out != OutcomeError || !errors.Is(err, boom) || sends(port.Calls()) != 0 || tt.store.marks != 0 {
				t.Fatalf("out=%q err=%v sends=%d marks=%d", out, err, sends(port.Calls()), tt.store.marks)
			}
		})
	}
}

func TestWelcomerFollowsConfigReload(t *testing.T) {
	off := welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(false), Text: "hello"})
	on := welcomeCfg("auto", nil, onWelcome("hello"))
	live := config.NewStore(off)
	st := &memWelcome{}
	port := fake.New()
	w := &Welcomer{Config: live, Store: st, Port: port}
	step := func(user int64, want Outcome) {
		t.Helper()
		out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: user})
		if err != nil || out != want {
			t.Fatalf("user %d = %q err=%v, want %q", user, out, err, want)
		}
	}
	step(1, OutcomeSkipDisabled)
	live.Swap(on)
	step(1, OutcomeSent)
	live.Swap(off)
	step(2, OutcomeSkipDisabled)
	if sends(port.Calls()) != 1 {
		t.Fatalf("sends=%d, want 1", sends(port.Calls()))
	}
}

func TestWelcomerRateCapInsideWindow(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	port := fake.New()
	w := newW(welcomeCfg("auto", nil, config.Welcome{
		Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(1),
	}), &memWelcome{}, port, nil)
	w.Now = func() time.Time { return now }
	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeSent || sends(port.Calls()) != 1 {
		t.Fatalf("initial send=%q err=%v sends=%d, want sent and 1 send", out, err, sends(port.Calls()))
	}
	now = now.Add(59*time.Second + 500*time.Millisecond)
	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 2})
	if err != nil || out != OutcomeSkipRateCapped || sends(port.Calls()) != 1 {
		t.Fatalf("at 59.5s=%q err=%v sends=%d, want skip_rate_capped and 1 send", out, err, sends(port.Calls()))
	}
}

func TestWelcomerRateCapWindowEdge(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	port := fake.New()
	w := newW(welcomeCfg("auto", nil, config.Welcome{
		Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(1),
	}), &memWelcome{}, port, nil)
	w.Now = func() time.Time { return now }
	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeSent || sends(port.Calls()) != 1 {
		t.Fatalf("initial send=%q err=%v sends=%d, want sent and 1 send", out, err, sends(port.Calls()))
	}
	now = now.Add(60 * time.Second)
	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 2})
	if err != nil || out != OutcomeSent || sends(port.Calls()) != 2 {
		t.Fatalf("at 60s=%q err=%v sends=%d, want sent and 2 sends", out, err, sends(port.Calls()))
	}
}

func TestWelcomerNotAdmittedBeforeDisabled(t *testing.T) {
	port := fake.New()
	cfg := welcomeCfg("allowlist", []int64{-200}, config.Welcome{Enabled: boolPtr(false), Text: "hello"})
	out, err := newW(cfg, &memWelcome{}, port, nil).
		Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
	if err != nil || out != OutcomeSkipNotAdmitted || sends(port.Calls()) != 0 {
		t.Fatalf("out=%q err=%v sends=%d, want skip_not_admitted and no sends", out, err, sends(port.Calls()))
	}
}

func TestWelcomerDisabledBeforeChatRow(t *testing.T) {
	for _, tt := range []struct {
		name  string
		store *memWelcome
	}{
		{"disabled row", disabledRow()},
		{"chat read error", &memWelcome{errChat: errors.New("chat read failed")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port := fake.New()
			cfg := welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(false), Text: "hello"})
			out, err := newW(cfg, tt.store, port, nil).
				Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
			if err != nil || out != OutcomeSkipDisabled || sends(port.Calls()) != 0 {
				t.Fatalf("out=%q err=%v sends=%d, want skip_disabled, nil error and no sends", out, err, sends(port.Calls()))
			}
		})
	}
}

func TestWelcomerChatRowBeforeBlocklist(t *testing.T) {
	port := fake.New()
	out, err := newW(welcomeCfg("auto", nil, onWelcome("hello")), disabledRow(), port, listedIDs{7: true}).
		Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
	if err != nil || out != OutcomeSkipChatDisabled || sends(port.Calls()) != 0 {
		t.Fatalf("out=%q err=%v sends=%d, want skip_chat_disabled and no sends", out, err, sends(port.Calls()))
	}
}

func TestWelcomerKnownBeforeRateCap(t *testing.T) {
	for _, tt := range []struct {
		name  string
		store *memWelcome
	}{
		{"welcomed", &memWelcome{welcomed: map[[2]int64]bool{{-100, 7}: true}}},
		{"trusted", &memWelcome{trust: map[[2]int64]int{{-100, 7}: 1}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port := fake.New()
			cfg := welcomeCfg("auto", nil, config.Welcome{
				Enabled: boolPtr(true), Text: "hello", MaxPerMinute: intPtr(1),
			})
			w := newW(cfg, tt.store, port, nil)
			w.Now = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }
			out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
			if err != nil || out != OutcomeSent || sends(port.Calls()) != 1 {
				t.Fatalf("initial send=%q err=%v sends=%d, want sent and 1 send", out, err, sends(port.Calls()))
			}
			out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
			if err != nil || out != OutcomeSkipKnown || sends(port.Calls()) != 1 {
				t.Fatalf("known user at cap=%q err=%v sends=%d, want skip_known and 1 send", out, err, sends(port.Calls()))
			}
		})
	}
}
