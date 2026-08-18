// Package detect: this file flattens a domain.Message into the single
// NormalizedMessage representation every detector runs over, so detectors
// don't each re-implement text concatenation, entity scanning, and
// deobfuscation.
package detect

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// NormalizedMessage is the flattened, deobfuscated view of a domain.Message.
type NormalizedMessage struct {
	Text           string
	Links          []string
	Mentions       []string
	HasCustomEmoji bool
	SenderTagNorm  string
	RawLen         int
}

// urlRe matches http(s) URLs anywhere in raw text.
// tMeRe matches bare t.me/... tokens (word-boundary anchored so "t.me/x" is
// not matched inside a longer word like "beat.me/x").
// mentionRe matches @handle tokens.
var (
	urlRe     = regexp.MustCompile(`https?://\S+`)
	tMeRe     = regexp.MustCompile(`\bt\.me/\S+`)
	mentionRe = regexp.MustCompile(`@\w+`)
)

// Normalize flattens m into a NormalizedMessage: it concatenates every
// visible text source (message text, external-reply preview, poll option
// texts, sender tag) into one blob, deobfuscates it, and pre-extracts
// links, mentions, and the custom-emoji flag so detectors can work off one
// consistent shape.
func Normalize(m domain.Message) NormalizedMessage {
	parts := make([]string, 0, 3+len(m.PollOptionTexts))
	parts = append(parts, m.Text, m.ExternalReplyText)
	parts = append(parts, m.PollOptionTexts...)
	parts = append(parts, m.SenderTag)
	raw := strings.Join(parts, " ")

	hasCustomEmoji := false
	for _, e := range m.Entities {
		if e.Type == "custom_emoji" {
			hasCustomEmoji = true
			break
		}
	}

	return NormalizedMessage{
		Text:           Deobfuscate(raw),
		Links:          collectLinks(m.Entities, raw),
		Mentions:       collectMentions(raw),
		HasCustomEmoji: hasCustomEmoji,
		SenderTagNorm:  Deobfuscate(m.SenderTag),
		RawLen:         utf8.RuneCountInString(m.Text),
	}
}

// collectLinks gathers text_link URLs hidden behind entities (which would
// not otherwise appear as a URL-looking token in the raw text) plus
// URL-looking tokens scanned from the raw, pre-deobfuscation text (so
// percent-encoding and casing in the URL survive). Order is first-seen;
// duplicates are dropped.
func collectLinks(entities []domain.Entity, raw string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimRight(s, ".,;:!?)\"'")
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, e := range entities {
		if e.Type == "text_link" && e.URL != "" {
			add(e.URL)
		}
	}
	for _, s := range urlRe.FindAllString(raw, -1) {
		add(s)
	}
	for _, s := range tMeRe.FindAllString(raw, -1) {
		add(s)
	}
	return out
}

// collectMentions scans the raw text for @handle tokens. Order is
// first-seen; duplicates are dropped.
func collectMentions(raw string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, s := range mentionRe.FindAllString(raw, -1) {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
