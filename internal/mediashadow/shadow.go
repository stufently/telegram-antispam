// Package mediashadow labels the admin card of a captionless photo with what
// an image classifier (JEV) sees in the picture.
//
// It is a SHADOW: the verdict is already decided (review only, nothing
// applied) when it runs, and its answer only adds a line to the card and a
// signal to the audit row, so a moderator can sort cards and the operator can
// later compare the scores with the moderators' decisions. Every failure —
// no picture, download error, timeout, an API refusal, no key — is a silent
// skip: the card goes out exactly as it would have without the shadow.
package mediashadow

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// SignalName is the audit/card signal carrying the scores.
const SignalName = "jev_image"

const (
	// DefaultTimeout bounds the whole observation: getFile, download,
	// resize and the API call. It runs on the chat's sequencer, so it delays
	// that chat's card by at most this much and nothing else.
	DefaultTimeout = 8 * time.Second
	// maxDownload is above Telegram's own 10 MB cap for photos.
	maxDownload = 12 << 20
)

// Fetcher downloads a Telegram file (telegram.Port.DownloadFile).
type Fetcher interface {
	DownloadFile(ctx context.Context, fileID string, maxBytes int64) ([]byte, error)
}

// Classifier scores a prepared picture.
type Classifier interface {
	Classify(ctx context.Context, picture []byte, format string) (Scores, error)
}

// Shadow wires the pieces. Count, when set, receives "ok" or "failed" once
// per attempted observation.
type Shadow struct {
	Fetch    Fetcher
	Classify Classifier
	Timeout  time.Duration
	Count    func(result string)
}

// Label returns the jev_image signal for a review verdict on a captionless
// photo, or false when the shadow does not apply or failed.
func (s *Shadow) Label(ctx context.Context, m domain.Message, v domain.Verdict) (domain.Signal, bool) {
	if s == nil || !Eligible(m, v) {
		return domain.Signal{}, false
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	scores, err := s.observe(ctx, m.PhotoFileID)
	if err != nil {
		s.count("failed")
		log.Printf("chat=%d msg=%d: media shadow skipped: %v", m.ChatID, m.MessageID, err)
		return domain.Signal{}, false
	}
	s.count("ok")
	sig := domain.Signal{Name: SignalName, Detail: FormatDetail(scores)}
	log.Printf("chat=%d msg=%d: media shadow %s ms=%d (label only, no effect)",
		m.ChatID, m.MessageID, sig.Detail, time.Since(start).Milliseconds())
	return sig, true
}

func (s *Shadow) observe(ctx context.Context, fileID string) (Scores, error) {
	data, err := s.Fetch.DownloadFile(ctx, fileID, maxDownload)
	if err != nil {
		return Scores{}, err
	}
	picture, format, err := prepare(data)
	if err != nil {
		return Scores{}, err
	}
	return s.Classify.Classify(ctx, picture, format)
}

func (s *Shadow) count(result string) {
	if s.Count != nil {
		s.Count(result)
	}
}

// Eligible reports whether the shadow applies: a review-only verdict raised
// for captionless media, on a message that carries a photo. Stickers, videos
// and documents stay out — the classifier is asked about photos only.
func Eligible(m domain.Message, v domain.Verdict) bool {
	return v.ReviewOnly && v.Reason == "captionless_media" &&
		m.PhotoFileID != "" && slices.Contains(m.MediaKinds, "photo")
}

// FormatDetail renders the scores as the signal detail, which is persisted
// in the audit row and parsed back by the report.
func FormatDetail(s Scores) string {
	return fmt.Sprintf("promo=%.2f contact=%.2f ordinary=%.2f", s.Promo, s.Contact, s.Ordinary)
}

// ParseDetail reverses FormatDetail.
func ParseDetail(detail string) (Scores, bool) {
	var s Scores
	seen := map[string]bool{}
	for _, field := range strings.Fields(detail) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			return Scores{}, false
		}
		p, err := strconv.ParseFloat(v, 64)
		if err != nil || p < 0 || p > 1 {
			return Scores{}, false
		}
		switch k {
		case "promo":
			s.Promo = p
		case "contact":
			s.Contact = p
		case "ordinary":
			s.Ordinary = p
		default:
			continue
		}
		if seen[k] {
			return Scores{}, false
		}
		seen[k] = true
	}
	return s, len(seen) == 3
}

// CardLine is the line the admin card shows for the signal.
func CardLine(detail string) (string, bool) {
	s, ok := ParseDetail(detail)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("JEV photo (shadow, no effect): ad %.2f · contact %.2f · ordinary %.2f",
		s.Promo, s.Contact, s.Ordinary), true
}
