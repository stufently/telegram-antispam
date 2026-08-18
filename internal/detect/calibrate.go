package detect

// LabeledDoc is a single holdout example for calibrating/evaluating the
// naive-Bayes scorer: its tokens (already normalized/tokenized the same
// way as production input) plus the ground-truth label.
type LabeledDoc struct {
	Tokens []string
	Spam   bool
}

// Metrics is the confusion matrix and derived precision/recall from
// running the scorer over a labeled holdout set. TP/FP/TN/FN are counts
// of (predicted, actual) pairs: TP = predicted spam & actually spam,
// FP = predicted spam & actually ham, TN = predicted ham & actually ham,
// FN = predicted ham & actually spam.
type Metrics struct {
	TP, FP, TN, FN    int
	Precision, Recall float64
}

// Evaluate runs BayesIsSpam over each doc in labeled and tallies the
// resulting confusion matrix, then computes Precision = TP/(TP+FP) and
// Recall = TP/(TP+FN), each defined as 0.0 when its denominator is 0
// (rather than NaN) since there's nothing to measure in that case.
//
// Evaluate is pure: it only reads through src (BayesSource), never
// writes, and has no side effects of its own.
//
// If BayesIsSpam returns an error for a doc, that doc is skipped from
// the tally entirely rather than being counted as a ham prediction:
// scoring errors are unexpected against a fixed holdout set (the same
// src/scope combination that presumably works for every other doc), so
// silently folding them into "predicted ham" would be more likely to
// mask a real problem than to reflect a meaningful prediction. Skipping
// keeps a buggy/partial BayesSource from quietly inflating TN/FN counts.
func Evaluate(src BayesSource, scope string, labeled []LabeledDoc, vocabGuess int, threshold float64) Metrics {
	var m Metrics
	for _, doc := range labeled {
		isSpam, _, err := BayesIsSpam(src, scope, doc.Tokens, vocabGuess, threshold)
		if err != nil {
			continue
		}
		switch {
		case isSpam && doc.Spam:
			m.TP++
		case isSpam && !doc.Spam:
			m.FP++
		case !isSpam && !doc.Spam:
			m.TN++
		case !isSpam && doc.Spam:
			m.FN++
		}
	}
	if m.TP+m.FP > 0 {
		m.Precision = float64(m.TP) / float64(m.TP+m.FP)
	}
	if m.TP+m.FN > 0 {
		m.Recall = float64(m.TP) / float64(m.TP+m.FN)
	}
	return m
}
