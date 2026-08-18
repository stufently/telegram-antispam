// Package train implements idempotent sample import into the bayes feature
// store. It is an application package (unlike internal/detect, which stays
// pure): it wires detect's normalization/tokenization together with
// store's persistence to turn raw labeled text into training data.
package train

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

// SampleHash returns the sha256 hex digest of text after detect.Deobfuscate
// is applied. It is a *sample* fingerprint used purely for import dedup
// (the samples table's unique normalized_hash column) — not a message
// identity, and not related to any incident key.
func SampleHash(text string) string {
	sum := sha256.Sum256([]byte(detect.Deobfuscate(text)))
	return hex.EncodeToString(sum[:])
}

// ImportSample records one labeled training sample. It normalizes and
// tokenizes text, then atomically inserts its hash into the samples table
// and, if the hash is new (added), bumps the sample's tokens into the
// bayes feature store — both in one transaction via db.RecordSample, so a
// sample row can never commit without its token counts (or vice versa). If
// the hash already exists, nothing is bumped — a re-import of the same
// text is a no-op, so repeated imports never double-count a sample's
// tokens.
func ImportSample(db *store.DB, scope, label, origin, text string) (bool, error) {
	n := detect.Normalize(domain.Message{Text: text})
	tokens := detect.Tokenize(n)
	hash := SampleHash(text)

	return db.RecordSample(scope, label, origin, hash, tokens)
}

// ImportFile imports one sample per non-empty (trimmed) line of the file at
// path, returning how many were newly added versus already-known (skipped).
func ImportFile(db *store.DB, scope, label, origin, path string) (added, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		isAdded, err := ImportSample(db, scope, label, origin, line)
		if err != nil {
			return added, skipped, fmt.Errorf("import %s line %d: %w", path, lineNo, err)
		}
		if isAdded {
			added++
		} else {
			skipped++
		}
	}
	if err := scanner.Err(); err != nil {
		return added, skipped, err
	}
	return added, skipped, nil
}
