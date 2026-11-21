package mediashadow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

type stubFetch struct {
	data []byte
	err  error
	got  string
}

func (f *stubFetch) DownloadFile(_ context.Context, id string, max int64) ([]byte, error) {
	f.got = id
	if max <= 0 {
		return nil, errors.New("no limit")
	}
	return f.data, f.err
}

type stubJEV struct {
	s      Scores
	err    error
	block  bool
	format string
	calls  int
}

func (j *stubJEV) Classify(ctx context.Context, _ []byte, format string) (Scores, error) {
	j.calls++
	j.format = format
	if j.block {
		<-ctx.Done()
		return Scores{}, ctx.Err()
	}
	return j.s, j.err
}

func photoMsg() domain.Message {
	return domain.Message{ChatID: -100, MessageID: 5, MediaKinds: []string{"photo"}, PhotoFileID: "AgAD-big"}
}

func reviewVerdict() domain.Verdict {
	return domain.Verdict{Action: domain.ActionQuarantine, Reason: "captionless_media", ReviewOnly: true,
		Signals: []domain.Signal{{Name: "captionless_media"}}}
}

func newShadow(t *testing.T, f *stubFetch, j *stubJEV, counts *[]string) *Shadow {
	t.Helper()
	return &Shadow{Fetch: f, Classify: j, Timeout: time.Second, Count: func(r string) { *counts = append(*counts, r) }}
}

func TestLabelReturnsTheScoresAsASignal(t *testing.T) {
	var counts []string
	f := &stubFetch{data: jpegOf(t, 400, 300)}
	j := &stubJEV{s: Scores{Promo: .97, Contact: .5, Ordinary: .031}}
	sig, ok := newShadow(t, f, j, &counts).Label(context.Background(), photoMsg(), reviewVerdict())
	if !ok || sig.Name != "jev_image" || sig.Detail != "promo=0.97 contact=0.50 ordinary=0.03" {
		t.Fatalf("sig=%+v ok=%v", sig, ok)
	}
	if f.got != "AgAD-big" || j.format != "jpeg" || len(counts) != 1 || counts[0] != "ok" {
		t.Fatalf("fetched %q format %q counts %v", f.got, j.format, counts)
	}
}

func TestLabelSkipsSilentlyOnEveryFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		f *stubFetch
		j *stubJEV
	}{
		"download":      {&stubFetch{err: errors.New("telegram: getFile: refused")}, &stubJEV{}},
		"not a picture": {&stubFetch{data: []byte("webp?")}, &stubJEV{}},
		"api":           {nil, &stubJEV{err: errors.New("jev: status 400")}},
		"timeout":       {nil, &stubJEV{block: true}},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.f == nil {
				tc.f = &stubFetch{data: jpegOf(t, 300, 300)}
			}
			var counts []string
			s := newShadow(t, tc.f, tc.j, &counts)
			s.Timeout = 50 * time.Millisecond
			start := time.Now()
			if sig, ok := s.Label(context.Background(), photoMsg(), reviewVerdict()); ok || sig != (domain.Signal{}) {
				t.Fatalf("failure produced a label: %+v", sig)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("timeout not applied")
			}
			if len(counts) != 1 || counts[0] != "failed" {
				t.Fatalf("counts = %v", counts)
			}
		})
	}
}

func TestLabelOnlyForCaptionlessPhotoReviews(t *testing.T) {
	cases := map[string]func(*domain.Message, *domain.Verdict){
		"not review only": func(_ *domain.Message, v *domain.Verdict) { v.ReviewOnly = false },
		"keyboard review": func(_ *domain.Message, v *domain.Verdict) { v.Reason = "bot_keyboard" },
		"sticker":         func(m *domain.Message, _ *domain.Verdict) { m.MediaKinds = []string{"sticker"} },
		"no file id":      func(m *domain.Message, _ *domain.Verdict) { m.PhotoFileID = "" },
		"no photo kind":   func(m *domain.Message, _ *domain.Verdict) { m.MediaKinds = nil },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			m, v := photoMsg(), reviewVerdict()
			mod(&m, &v)
			var counts []string
			j := &stubJEV{s: Scores{Promo: 1}}
			if _, ok := newShadow(t, &stubFetch{data: jpegOf(t, 300, 300)}, j, &counts).Label(context.Background(), m, v); ok || j.calls != 0 || len(counts) != 0 {
				t.Fatalf("shadow ran: ok=%v calls=%d counts=%v", ok, j.calls, counts)
			}
		})
	}
	var nilShadow *Shadow
	if _, ok := nilShadow.Label(context.Background(), photoMsg(), reviewVerdict()); ok {
		t.Fatal("nil shadow labelled")
	}
}

func TestDetailRoundTripAndRejects(t *testing.T) {
	in := Scores{Promo: .97, Contact: 0, Ordinary: 1}
	if got, ok := ParseDetail(FormatDetail(in)); !ok || got != in {
		t.Fatalf("round trip: %+v %v", got, ok)
	}
	for _, bad := range []string{"", "promo=0.9 contact=0.1", "promo=1.1 contact=0 ordinary=0",
		"promo=x contact=0 ordinary=0", "promo=0.1 contact=0.1 ordinary", "promo=-0.1 contact=0 ordinary=0", "promo=0.1 promo=0.2 contact=0 ordinary=0"} {
		if _, ok := ParseDetail(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	if line, ok := CardLine("promo=0.97 contact=0.12 ordinary=0.04"); !ok ||
		line != "JEV photo (shadow, no effect): ad 0.97 · contact 0.12 · ordinary 0.04" {
		t.Fatalf("card line %q", line)
	}
}
