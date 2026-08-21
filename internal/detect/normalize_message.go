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
		Links:          collectLinks(m.Entities, m.Text, raw),
		Mentions:       collectMentions(raw),
		HasCustomEmoji: hasCustomEmoji,
		SenderTagNorm:  Deobfuscate(m.SenderTag),
		RawLen:         utf8.RuneCountInString(m.Text),
	}
}

// collectLinks gathers, in order: text_link URLs hidden behind entities
// (which would not otherwise appear as a URL-looking token in the raw
// text), the text spans of plain "url" entities, and URL-looking tokens
// scanned from the raw, pre-deobfuscation text (so percent-encoding and
// casing in the URL survive). Order is first-seen; duplicates are dropped.
//
// The "url" entity branch is what covers a link written WITHOUT a scheme —
// "bit.ly/x", "crypto-bot.org". Telegram marks those as Type "url" with an
// empty URL field (that field is only populated for text_link) and leaves
// the address in the message text, where neither urlRe (which requires
// http[s]://) nor tMeRe finds it. Without this branch such a link was
// invisible to block_links_for_untrusted and banned_domains — i.e. exactly
// the shape a spammer types.
//
// entityText is the string the entities index into (the message text or
// caption), NOT the joined blob: offsets are relative to that one field.
func collectLinks(entities []domain.Entity, entityText, raw string) []string {
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
		switch {
		case e.Type == "text_link" && e.URL != "":
			add(e.URL)
		case e.Type == "url":
			add(entitySpan(entityText, e.Offset, e.Length))
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

// entitySpan returns the substring of s that a Telegram message entity
// covers. Offsets and lengths in the Bot API are counted in UTF-16 code
// units, not bytes and not runes, so they cannot index a Go string
// directly: any emoji or non-BMP character earlier in the message shifts
// every later offset by one unit per surrogate pair. Out-of-range values
// yield an empty string rather than a panic — a malformed entity must not
// take the process down.
func entitySpan(s string, offset, length int) string {
	if offset < 0 || length <= 0 {
		return ""
	}
	units := 0
	start, end := -1, -1
	for i, r := range s {
		if units == offset && start < 0 {
			start = i
		}
		if units == offset+length && end < 0 {
			end = i
			break
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	if start < 0 {
		if units == offset {
			start = len(s)
		} else {
			return ""
		}
	}
	if end < 0 {
		if units != offset+length {
			return ""
		}
		end = len(s)
	}
	return s[start:end]
}
