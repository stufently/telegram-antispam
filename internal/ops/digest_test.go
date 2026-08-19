package ops

import (
	"context"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

func TestBuildDigestWithCounts(t *testing.T) {
	text := BuildDigest(map[string]int{"ban": 2, "mute": 1}, nil, nil, "last 24h")

	for _, want := range []string{"ban 2", "mute 1", "total 3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("BuildDigest() = %q, want it to contain %q", text, want)
		}
	}
}

func TestBuildDigestEmptyCounts(t *testing.T) {
	text := BuildDigest(map[string]int{}, nil, nil, "last 24h")

	if !strings.Contains(strings.ToLower(text), "no incidents") {
		t.Fatalf("BuildDigest(empty) = %q, want a no-incidents line", text)
	}
}

func TestBuildDigestDeterministicOrder(t *testing.T) {
	counts := map[string]int{"mute": 3, "ban": 12, "delete_mute": 34}
	for i := 0; i < 5; i++ {
		text := BuildDigest(counts, nil, nil, "last 24h")
		banIdx := strings.Index(text, "ban 12")
		deleteMuteIdx := strings.Index(text, "delete_mute 34")
		muteIdx := strings.Index(text, "mute 3")
		if banIdx == -1 || deleteMuteIdx == -1 || muteIdx == -1 {
			t.Fatalf("BuildDigest() = %q, missing expected substrings", text)
		}
		if !(banIdx < deleteMuteIdx && deleteMuteIdx < muteIdx) {
			t.Fatalf("BuildDigest() = %q, want alphabetical order ban < delete_mute < mute", text)
		}
	}
}

type fakeAdminSender struct {
	calls    int
	lastChat int64
	lastMsg  telegram.AdminMessage
}

func (f *fakeAdminSender) SendAdmin(ctx context.Context, adminChat int64, msg telegram.AdminMessage) (int, error) {
	f.calls++
	f.lastChat = adminChat
	f.lastMsg = msg
	return 42, nil
}

type fakeDigestSource struct {
	counts     map[string]int
	dryRun     map[string]int
	incomplete map[string]int
	err        error
	gotTS      int64
}

func (f *fakeDigestSource) ActionCountsSince(ts int64) (map[string]int, map[string]int, map[string]int, error) {
	f.gotTS = ts
	return f.counts, f.dryRun, f.incomplete, f.err
}

func TestSendDigestSendsOneMessageWithBuiltText(t *testing.T) {
	sender := &fakeAdminSender{}
	scripted := map[string]int{"ban": 5, "kick": 2}
	src := &fakeDigestSource{counts: scripted}

	const now int64 = 1000000000
	if err := SendDigest(context.Background(), sender, 777, src, now); err != nil {
		t.Fatal(err)
	}

	if sender.calls != 1 {
		t.Fatalf("SendAdmin calls = %d, want 1", sender.calls)
	}
	if sender.lastChat != 777 {
		t.Fatalf("adminChat = %d, want 777", sender.lastChat)
	}
	want := BuildDigest(scripted, nil, nil, "last 24h")
	if sender.lastMsg.Text != want {
		t.Fatalf("Text = %q, want %q", sender.lastMsg.Text, want)
	}
	if src.gotTS != now-86400 {
		t.Fatalf("since = %d, want %d", src.gotTS, now-86400)
	}
}

// Dry-run audit rows record actions that were deliberately NOT carried out.
// Reporting them in the headline count would tell an operator tuning a new
// chat that dozens of bans happened when zero did.
func TestBuildDigestKeepsDryRunOutOfAppliedTotal(t *testing.T) {
	text := BuildDigest(map[string]int{"ban": 1}, map[string]int{"ban": 37}, nil, "last 24h")

	appliedIdx := strings.Index(text, "ban 1")
	dryIdx := strings.Index(text, "ban 37")
	if appliedIdx == -1 || dryIdx == -1 {
		t.Fatalf("BuildDigest() = %q, want both applied and dry-run counts", text)
	}
	if !strings.Contains(text, "total 1") {
		t.Fatalf("BuildDigest() = %q, want the applied total to exclude dry-run rows", text)
	}
	if !strings.Contains(text, "dry-run") {
		t.Fatalf("BuildDigest() = %q, want the dry-run counts labeled", text)
	}
	if appliedIdx > dryIdx {
		t.Fatalf("BuildDigest() = %q, want applied counts before dry-run ones", text)
	}
}

// A dry-run-only day is not "no incidents", and it is not "37 bans" either.
func TestBuildDigestDryRunOnlyReportsNoActionsApplied(t *testing.T) {
	text := BuildDigest(nil, map[string]int{"ban": 37}, nil, "last 24h")

	if !strings.Contains(text, "no actions applied") {
		t.Fatalf("BuildDigest() = %q, want it to state that nothing was applied", text)
	}
	if !strings.Contains(text, "ban 37") {
		t.Fatalf("BuildDigest() = %q, want the simulated count reported", text)
	}
}

// A live incident that never reached an acted state is neither applied nor
// simulated; it must be reported as its own category rather than inflating
// the applied total.
func TestBuildDigestReportsIncompleteSeparately(t *testing.T) {
	text := BuildDigest(map[string]int{"ban": 2}, nil, map[string]int{"ban": 5}, "last 24h")

	if !strings.Contains(text, "total 2") {
		t.Fatalf("BuildDigest() = %q, want the applied total to exclude incomplete rows", text)
	}
	if !strings.Contains(text, "incomplete") || !strings.Contains(text, "ban 5") {
		t.Fatalf("BuildDigest() = %q, want the incomplete count labeled", text)
	}
}
