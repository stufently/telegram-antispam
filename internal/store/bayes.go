package store

import "database/sql"

// bumpBayesTx applies one labeled sample's tokens to the naive Bayes
// feature store on an already-open transaction, so callers that need the
// bump to commit atomically with another write (see RecordSample) can
// share their tx instead of opening a second one. Each token is bumped
// once per occurrence in tokens, so a token repeated within the same
// sample accrues multiple counts (e.g. tokens=["a","a"] bumps "a"'s count
// by 2). bayes_totals.docs is incremented once per call and
// bayes_totals.tokens is incremented by len(tokens).
func bumpBayesTx(tx *sql.Tx, scope, label string, tokens []string) error {
	for _, tok := range tokens {
		if _, err := tx.Exec(`
INSERT INTO bayes_tokens(scope, token, label, count)
VALUES(?, ?, ?, 1)
ON CONFLICT(scope, token, label) DO UPDATE SET
	count = count + 1`,
			scope, tok, label,
		); err != nil {
			return err
		}
	}

	_, err := tx.Exec(`
INSERT INTO bayes_totals(scope, label, docs, tokens)
VALUES(?, ?, 1, ?)
ON CONFLICT(scope, label) DO UPDATE SET
	docs   = docs + 1,
	tokens = tokens + excluded.tokens`,
		scope, label, len(tokens),
	)
	return err
}

// BumpBayes records one labeled training sample's tokens into the naive
// Bayes feature store, all within a single transaction. See bumpBayesTx for
// the counting rules.
func (db *DB) BumpBayes(scope, label string, tokens []string) error {
	return db.Write(func(tx *sql.Tx) error {
		return bumpBayesTx(tx, scope, label, tokens)
	})
}

// TokenCounts reads the spam and ham counts for the given scope and
// tokens, returning maps keyed by token. A token with no stored count for
// a label is simply absent from that label's map, so a lookup on the
// returned map yields 0 for it (Go's zero value for int).
func (db *DB) TokenCounts(scope string, tokens []string) (spam, ham map[string]int, err error) {
	spam = make(map[string]int, len(tokens))
	ham = make(map[string]int, len(tokens))
	if len(tokens) == 0 {
		return spam, ham, nil
	}

	query, args := tokenCountsQuery(scope, tokens)
	rows, err := db.Read().Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var token, label string
		var count int
		if err := rows.Scan(&token, &label, &count); err != nil {
			return nil, nil, err
		}
		switch label {
		case "spam":
			spam[token] = count
		case "ham":
			ham[token] = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return spam, ham, nil
}

// tokenCountsQuery builds the SELECT and its arg list for TokenCounts,
// using a placeholder per distinct token requested.
func tokenCountsQuery(scope string, tokens []string) (string, []any) {
	placeholders := make([]byte, 0, len(tokens)*2)
	args := make([]any, 0, len(tokens)+1)
	args = append(args, scope)
	for i, tok := range tokens {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, tok)
	}
	query := `SELECT token, label, count FROM bayes_tokens WHERE scope=? AND token IN (` +
		string(placeholders) + `)`
	return query, args
}

// BayesTotals reads the per-label document and token totals for scope,
// returning 0 for any total whose row does not exist yet.
func (db *DB) BayesTotals(scope string) (spamDocs, hamDocs, spamTok, hamTok int, err error) {
	rows, err := db.Read().Query(
		`SELECT label, docs, tokens FROM bayes_totals WHERE scope=?`, scope,
	)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var label string
		var docs, tok int
		if err := rows.Scan(&label, &docs, &tok); err != nil {
			return 0, 0, 0, 0, err
		}
		switch label {
		case "spam":
			spamDocs, spamTok = docs, tok
		case "ham":
			hamDocs, hamTok = docs, tok
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, 0, err
	}
	return spamDocs, hamDocs, spamTok, hamTok, nil
}

// dropBayesTx removes one labeled sample's tokens from the feature store —
// the exact inverse of bumpBayesTx — on an already-open transaction.
//
// Counts are floored at zero rather than allowed to go negative. A negative
// count would not merely be wrong arithmetic: bayesScore reads these
// aggregates as evidence, and a negative "evidence" flips the sign of a
// term, which is a stronger claim than "no evidence at all". Flooring keeps
// a corrupted or double-reversed corpus merely inaccurate instead of
// actively misleading.
func dropBayesTx(tx *sql.Tx, scope, label string, tokens []string) error {
	for _, tok := range tokens {
		if _, err := tx.Exec(`
UPDATE bayes_tokens SET count = MAX(0, count - 1)
WHERE scope = ? AND token = ? AND label = ?`,
			scope, tok, label,
		); err != nil {
			return err
		}
	}

	_, err := tx.Exec(`
UPDATE bayes_totals
SET docs   = MAX(0, docs - 1),
    tokens = MAX(0, tokens - ?)
WHERE scope = ? AND label = ?`,
		len(tokens), scope, label,
	)
	return err
}
