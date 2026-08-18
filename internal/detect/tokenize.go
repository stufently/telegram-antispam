package detect

import (
	"strings"
	"unicode"
)

// Tokenize produces deterministic feature tokens from a normalized message.
// Tokens include:
// - word unigrams (length >= 2)
// - word bigrams (adjacent pairs, prefixed "bi:")
// - link/domain features ("host:"+host for each link, "has:link" if any)
// - meta features ("has:custom_emoji", "has:mention", "len:short", "len:long")
func Tokenize(n NormalizedMessage) []string {
	var tokens []string

	// 1. Extract unigrams (word tokens of length >= 2)
	unigrams := extractUnigrams(n.Text)

	// 2. Add unigrams to tokens
	tokens = append(tokens, unigrams...)

	// 3. Add bigrams (adjacent unigram pairs)
	for i := 0; i < len(unigrams)-1; i++ {
		bigram := "bi:" + unigrams[i] + "_" + unigrams[i+1]
		tokens = append(tokens, bigram)
	}

	// 4. Add link features
	if len(n.Links) > 0 {
		tokens = append(tokens, "has:link")
		for _, link := range n.Links {
			host := extractHost(link)
			if host != "" {
				tokens = append(tokens, "host:"+host)
			}
		}
	}

	// 5. Add meta features
	if n.HasCustomEmoji {
		tokens = append(tokens, "has:custom_emoji")
	}

	if len(n.Mentions) > 0 {
		tokens = append(tokens, "has:mention")
	}

	if n.RawLen <= 10 {
		tokens = append(tokens, "len:short")
	}

	if n.RawLen >= 200 {
		tokens = append(tokens, "len:long")
	}

	return tokens
}

// extractUnigrams splits text on non-alphanumeric boundaries (unicode-aware)
// and returns tokens of length >= 2.
func extractUnigrams(text string) []string {
	var unigrams []string
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	for _, token := range tokens {
		if len(token) >= 2 {
			unigrams = append(unigrams, strings.ToLower(token))
		}
	}

	return unigrams
}
