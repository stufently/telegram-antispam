package watch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// *store.DB must keep satisfying WelcomeStore: the wiring passes it directly.
var _ WelcomeStore = (*store.DB)(nil)

type memWelcome struct {
	mu       sync.Mutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.welcomed[[2]int64{chatID, userID}], nil
}

func (m *memWelcome) MarkWelcomed(chatID, userID int64) error {
	if m.errMark != nil {
		return m.errMark
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trust[[2]int64{chatID, userID}], nil
}

func (m *memWelcome) GetChat(chatID int64) (store.ChatRow, bool, error) {
	if m.errChat != nil {
		return store.ChatRow{}, false, m.errChat
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	return &config.Config{
		Chats:   config.ChatsPolicy{Mode: mode, Allowlist: allow},
		Welcome: w,
	}
}

func countWelcome(calls []string) int {
	n := 0
	for _, c := range calls {
		if c == "SendWelcome" {
			n++
		}
	}
	return n
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
	w := &Welcomer{
		Config: config.NewStore(welcomeCfg("auto", nil, config.Welcome{
			Enabled: boolPtr(false),
			Text:    "global text that must not be sent",
			Chats: map[int64]config.WelcomeChat{
				-100: {Enabled: boolPtr(true), Text: "  rules for A. mistaken mute: @desk  "},
				-200: {Enabled: boolPtr(true), Text: "rules for B"},
			},
		})),
		Store:     st,
		Port:      port,
		Blocklist: listedIDs{},
		Now:       func() time.Time { return time.Unix(1_700_000_000, 0) },
	}

	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
	if err != nil || out != OutcomeSent {
		t.Fatalf("first = %q err=%v, want sent", out, err)
	}
	if port.LastWelcome.Text != "rules for A. mistaken mute: @desk" || port.LastWelcome.Chat != -100 || port.LastWelcome.UserID != 7 {
		t.Fatalf("sent %+v, want the trimmed per-chat text to user 7 in chat -100", port.LastWelcome)
	}

	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
	if err != nil || out != OutcomeSkipKnown {
		t.Fatalf("repeat = %q err=%v, want skip_known", out, err)
	}

	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 8})
	if err != nil || out != OutcomeSent {
		t.Fatalf("other user = %q err=%v, want sent", out, err)
	}
	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -200, UserID: 7})
	if err != nil || out != OutcomeSent {
		t.Fatalf("other chat = %q err=%v, want sent", out, err)
	}
	if port.LastWelcome.Chat != -200 || port.LastWelcome.Text != "rules for B" {
		t.Fatalf("last send = %+v, want chat -200's own text", port.LastWelcome)
	}
	if got := countWelcome(port.Calls()); got != 3 {
		t.Fatalf("SendWelcome calls = %d, want 3 (the repeat must not send)", got)
	}
	if st.marks != 3 {
		t.Fatalf("marks = %d, want 3", st.marks)
	}
}

func TestWelcomerSkips(t *testing.T) {
	enabled := config.Welcome{
		Enabled: boolPtr(true),
		Text:    "hello",
	}
	cases := []struct {
		name  string
		cfg   *config.Config
		store *memWelcome
		list  listedIDs
		user  int64
		want  Outcome
	}{
		{
			name:  "not admitted",
			cfg:   welcomeCfg("allowlist", []int64{-1}, enabled),
			store: &memWelcome{},
			want:  OutcomeSkipNotAdmitted,
		},
		{
			name:  "disabled",
			cfg:   welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(false), Text: "hello"}),
			store: &memWelcome{},
			want:  OutcomeSkipDisabled,
		},
		{
			name: "chat row disabled",
			cfg:  welcomeCfg("auto", nil, enabled),
			store: &memWelcome{
				chats: map[int64]store.ChatRow{-100: {Enabled: false, DryRun: false}},
				found: map[int64]bool{-100: true},
			},
			want: OutcomeSkipChatDisabled,
		},
		{
			name:  "blocklisted",
			cfg:   welcomeCfg("auto", nil, enabled),
			store: &memWelcome{},
			list:  listedIDs{7: true},
			user:  7,
			want:  OutcomeSkipBlocklisted,
		},
		{
			name: "already welcomed",
			cfg:  welcomeCfg("auto", nil, enabled),
			store: &memWelcome{
				welcomed: map[[2]int64]bool{{-100, 7}: true},
			},
			user: 7,
			want: OutcomeSkipKnown,
		},
		{
			name: "already trusted",
			cfg:  welcomeCfg("auto", nil, enabled),
			store: &memWelcome{
				trust: map[[2]int64]int{{-100, 7}: 1},
			},
			user: 7,
			want: OutcomeSkipKnown,
		},
		{
			name: "admission is decided before the switch",
			cfg:  welcomeCfg("allowlist", nil, config.Welcome{Enabled: boolPtr(false)}),
			store: &memWelcome{
				chats: map[int64]store.ChatRow{-100: {Enabled: false}},
				found: map[int64]bool{-100: true},
			},
			list: listedIDs{7: true},
			user: 7,
			want: OutcomeSkipNotAdmitted,
		},
		{
			name: "switch is decided before the stored chat row",
			cfg:  welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(false), Text: "hello"}),
			store: &memWelcome{
				chats: map[int64]store.ChatRow{-100: {Enabled: false}},
				found: map[int64]bool{-100: true},
			},
			want: OutcomeSkipDisabled,
		},
		{
			name: "stored chat row is decided before the blocklist",
			cfg:  welcomeCfg("auto", nil, enabled),
			store: &memWelcome{
				chats: map[int64]store.ChatRow{-100: {Enabled: false}},
				found: map[int64]bool{-100: true},
			},
			list: listedIDs{7: true},
			user: 7,
			want: OutcomeSkipChatDisabled,
		},
		{
			name: "blocklist is decided before known",
			cfg:  welcomeCfg("auto", nil, enabled),
			store: &memWelcome{
				welcomed: map[[2]int64]bool{{-100, 7}: true},
				trust:    map[[2]int64]int{{-100, 7}: 4},
			},
			list: listedIDs{7: true},
			user: 7,
			want: OutcomeSkipBlocklisted,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			port := fake.New()
			user := tt.user
			if user == 0 {
				user = 7
			}
			w := &Welcomer{
				Config:    config.NewStore(tt.cfg),
				Store:     tt.store,
				Port:      port,
				Blocklist: tt.list,
			}
			out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: user})
			if err != nil {
				t.Fatal(err)
			}
			if out != tt.want {
				t.Fatalf("outcome = %q, want %q", out, tt.want)
			}
			if got := countWelcome(port.Calls()); got != 0 {
				t.Fatalf("SendWelcome calls = %d, want 0", got)
			}
		})
	}
}

func TestWelcomerRateCap(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := &memWelcome{}
	port := fake.New()
	w := &Welcomer{
		Config: config.NewStore(welcomeCfg("auto", nil, config.Welcome{
			Enabled:      boolPtr(true),
			Text:         "hello",
			MaxPerMinute: intPtr(2),
		})),
		Store: st,
		Port:  port,
		Now:   func() time.Time { return now },
	}
	for i, user := range []int64{1, 2, 3} {
		out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: user})
		if err != nil {
			t.Fatal(err)
		}
		want := OutcomeSent
		if i == 2 {
			want = OutcomeSkipRateCapped
		}
		if out != want {
			t.Fatalf("user %d outcome = %q, want %q", user, out, want)
		}
	}
	if got := countWelcome(port.Calls()); got != 2 {
		t.Fatalf("sends inside the window = %d, want 2", got)
	}
	if st.marks != 2 {
		t.Fatalf("marks = %d, want 2 (the capped join must not be recorded)", st.marks)
	}

	now = now.Add(61 * time.Second)
	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 4})
	if err != nil || out != OutcomeSent {
		t.Fatalf("after 61s = %q err=%v, want sent", out, err)
	}
	if got := countWelcome(port.Calls()); got != 3 {
		t.Fatalf("sends after the window moved = %d, want 3", got)
	}
}

func TestWelcomerSendFailureNotMarked(t *testing.T) {
	st := &memWelcome{}
	port := fake.New()
	port.WelcomeErr = errors.New("telegram down")
	w := &Welcomer{
		Config: config.NewStore(welcomeCfg("auto", nil, config.Welcome{
			Enabled: boolPtr(true),
			Text:    "hello",
		})),
		Store: st,
		Port:  port,
	}
	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
	if out != OutcomeError || !errors.Is(err, port.WelcomeErr) {
		t.Fatalf("outcome = %q err=%v, want error wrapping the send failure", out, err)
	}
	if st.marks != 0 {
		t.Fatalf("marks = %d, want 0 after a failed send", st.marks)
	}
	known, err := st.WasWelcomed(-100, 7)
	if err != nil || known {
		t.Fatalf("was welcomed = %v err=%v, want false", known, err)
	}
}

func TestWelcomerStoreErrorFailsClosed(t *testing.T) {
	boom := errors.New("db")
	cases := []struct {
		name  string
		store *memWelcome
	}{
		{name: "get chat", store: &memWelcome{errChat: boom}},
		{name: "was welcomed", store: &memWelcome{errWas: boom}},
		{name: "trust count", store: &memWelcome{errTrust: boom}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			port := fake.New()
			w := &Welcomer{
				Config: config.NewStore(welcomeCfg("auto", nil, config.Welcome{
					Enabled: boolPtr(true),
					Text:    "hello",
				})),
				Store: tt.store,
				Port:  port,
			}
			out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 7})
			if out != OutcomeError || !errors.Is(err, boom) {
				t.Fatalf("outcome = %q err=%v, want the store error", out, err)
			}
			if got := countWelcome(port.Calls()); got != 0 {
				t.Fatalf("SendWelcome calls = %d, want 0", got)
			}
			if tt.store.marks != 0 {
				t.Fatalf("marks = %d, want 0", tt.store.marks)
			}
		})
	}
}

func TestWelcomerFollowsConfigReload(t *testing.T) {
	off := welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(false), Text: "hello"})
	on := welcomeCfg("auto", nil, config.Welcome{Enabled: boolPtr(true), Text: "hello"})
	live := config.NewStore(off)
	st := &memWelcome{}
	port := fake.New()
	w := &Welcomer{Config: live, Store: st, Port: port}

	out, err := w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeSkipDisabled {
		t.Fatalf("before reload = %q err=%v, want skip_disabled", out, err)
	}
	live.Swap(on)
	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 1})
	if err != nil || out != OutcomeSent {
		t.Fatalf("after enabling = %q err=%v, want sent", out, err)
	}
	live.Swap(off)
	out, err = w.Observe(context.Background(), telegram.JoinEvent{ChatID: -100, UserID: 2})
	if err != nil || out != OutcomeSkipDisabled {
		t.Fatalf("after disabling = %q err=%v, want skip_disabled", out, err)
	}
	if got := countWelcome(port.Calls()); got != 1 {
		t.Fatalf("SendWelcome calls = %d, want 1", got)
	}
}
