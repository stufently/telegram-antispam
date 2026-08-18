package detect

import "testing"

type fakeBayes struct {
	spam, ham map[string]int
	c         BayesCounts
}

func (f fakeBayes) TokenCounts(_ string, toks []string) (map[string]int, map[string]int, error) {
	s, h := map[string]int{}, map[string]int{}
	for _, t := range toks {
		s[t] = f.spam[t]
		h[t] = f.ham[t]
	}
	return s, h, nil
}

func (f fakeBayes) Totals(string) (BayesCounts, error) { return f.c, nil }

func TestBayesLogRatioSpamLeaning(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{"casino": 50}, ham: map[string]int{"casino": 0},
		c: BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	r, err := BayesLogRatio(f, "global", []string{"casino"}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r <= 0 {
		t.Fatalf("expected spam-leaning positive ratio, got %f", r)
	}
}

func TestBayesLogRatioEmptyCorpusNeutral(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{}, ham: map[string]int{},
		c: BayesCounts{},
	}
	r, err := BayesLogRatio(f, "global", []string{"anything"}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r != 0 {
		t.Fatalf("expected neutral 0 for empty corpus, got %f", r)
	}
}

func TestBayesLogRatioHamLeaning(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{"hello": 0}, ham: map[string]int{"hello": 80},
		c: BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	r, err := BayesLogRatio(f, "global", []string{"hello"}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r >= 0 {
		t.Fatalf("expected ham-leaning negative ratio, got %f", r)
	}
}

func TestBayesIsSpamThresholdBoundary(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{"casino": 50}, ham: map[string]int{"casino": 0},
		c: BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	ratio, err := BayesLogRatio(f, "global", []string{"casino"}, 1000)
	if err != nil {
		t.Fatal(err)
	}

	isSpam, r, err := BayesIsSpam(f, "global", []string{"casino"}, 1000, ratio)
	if err != nil {
		t.Fatal(err)
	}
	if r != ratio {
		t.Fatalf("expected returned ratio %f to match computed %f", r, ratio)
	}
	if !isSpam {
		t.Fatalf("expected threshold==ratio to count as spam (>=)")
	}

	isSpam, _, err = BayesIsSpam(f, "global", []string{"casino"}, 1000, ratio+0.0001)
	if err != nil {
		t.Fatal(err)
	}
	if isSpam {
		t.Fatalf("expected threshold slightly above ratio to not be spam")
	}
}

// TestBayesIsSpamEmptyCorpusNotSpam guards the fresh-deploy footgun: with an
// empty (untrained) corpus the neutral score is 0, and at the default
// threshold 0.0 a naive `0 >= 0` would flag every untrusted message. An
// empty corpus must never be classified as spam.
func TestBayesIsSpamEmptyCorpusNotSpam(t *testing.T) {
	f := fakeBayes{spam: map[string]int{}, ham: map[string]int{}, c: BayesCounts{}}
	isSpam, _, err := BayesIsSpam(f, "global", []string{"anything"}, 1000, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	if isSpam {
		t.Fatal("empty corpus must not be classified as spam at threshold 0")
	}
}

// TestBayesIsSpamNoTokensNotSpam: a message with no tokens (e.g. media-only)
// has no basis to score and must not be flagged even on a trained corpus at
// threshold 0.0, where the prior-difference tie could otherwise trip `>=`.
func TestBayesIsSpamNoTokensNotSpam(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{}, ham: map[string]int{},
		c: BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	isSpam, _, err := BayesIsSpam(f, "global", nil, 1000, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	if isSpam {
		t.Fatal("no tokens must not be classified as spam")
	}
}

// TestBayesIsSpamTrainedStillWorks confirms the empty-corpus/no-token guards
// don't suppress a genuine spam hit on a trained corpus.
func TestBayesIsSpamTrainedStillWorks(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{"casino": 50}, ham: map[string]int{"casino": 0},
		c: BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	isSpam, r, err := BayesIsSpam(f, "global", []string{"casino"}, 1000, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	if !isSpam || r <= 0 {
		t.Fatalf("trained spam token should flag: isSpam=%v ratio=%f", isSpam, r)
	}
}
