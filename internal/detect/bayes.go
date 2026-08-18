package detect

import "math"

// BayesCounts holds corpus-level totals used to compute naive Bayes
// priors and per-token likelihood denominators.
type BayesCounts struct {
	SpamDocs, HamDocs             int
	SpamTokenTotal, HamTokenTotal int
}

// BayesSource provides the token and corpus counts needed to score a set
// of tokens without loading the entire model. Implementations should only
// return counts for the requested tokens.
type BayesSource interface {
	TokenCounts(scope string, tokens []string) (spam map[string]int, ham map[string]int, err error)
	Totals(scope string) (BayesCounts, error)
}

// BayesLogRatio computes the log-space naive Bayes score for tokens
// against the spam/ham corpora identified by scope:
//
//	log P(tokens|spam) + log P(spam) - (log P(tokens|ham) + log P(ham))
//
// A positive result indicates a spam-leaning message; negative indicates
// ham-leaning; zero is neutral.
//
// Priors are Laplace-smoothed as (classDocs+1)/(SpamDocs+HamDocs+2). This
// avoids log(0) when one class (or both) has zero documents, and degrades
// gracefully to a 50/50 prior when both are zero. Per-token likelihoods
// use Laplace(+1) smoothing over (classTokenTotal + vocabGuess).
//
// If both corpora are entirely empty (SpamDocs+HamDocs==0), the result is
// defined as 0 (neutral) rather than relying on the smoothed prior alone,
// since there is no data at all to score against.
func BayesLogRatio(src BayesSource, scope string, tokens []string, vocabGuess int) (float64, error) {
	if vocabGuess < 1 {
		vocabGuess = 1
	}

	totals, err := src.Totals(scope)
	if err != nil {
		return 0, err
	}

	if totals.SpamDocs+totals.HamDocs == 0 {
		return 0, nil
	}

	spamCounts, hamCounts, err := src.TokenCounts(scope, tokens)
	if err != nil {
		return 0, err
	}

	// Laplace-smoothed priors avoid log(0) when one class has no documents.
	logPriorSpam := math.Log(float64(totals.SpamDocs+1) / float64(totals.SpamDocs+totals.HamDocs+2))
	logPriorHam := math.Log(float64(totals.HamDocs+1) / float64(totals.SpamDocs+totals.HamDocs+2))

	spamDenom := float64(totals.SpamTokenTotal + vocabGuess)
	hamDenom := float64(totals.HamTokenTotal + vocabGuess)

	logLikelihoodSpam := 0.0
	logLikelihoodHam := 0.0
	for _, tok := range tokens {
		logLikelihoodSpam += math.Log(float64(spamCounts[tok]+1) / spamDenom)
		logLikelihoodHam += math.Log(float64(hamCounts[tok]+1) / hamDenom)
	}

	return (logLikelihoodSpam + logPriorSpam) - (logLikelihoodHam + logPriorHam), nil
}

// BayesIsSpam scores tokens with BayesLogRatio and reports whether the
// result meets or exceeds threshold, along with the raw ratio.
func BayesIsSpam(src BayesSource, scope string, tokens []string, vocabGuess int, threshold float64) (bool, float64, error) {
	ratio, err := BayesLogRatio(src, scope, tokens, vocabGuess)
	if err != nil {
		return false, ratio, err
	}
	return ratio >= threshold, ratio, nil
}
