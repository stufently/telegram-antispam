package detect

import "testing"

// TestEvaluateCleanlySeparableToySet exercises Evaluate over a tiny
// hand-built holdout set where "casino" is spam-heavy and "hello" is
// ham-heavy (same fakeBayes shape as bayes_test.go), at a threshold that
// cleanly separates the two token classes (0.0, matching
// BayesThreshold's documented default). With 2 spam docs containing
// "casino" and 2 ham docs containing "hello", the scorer should get every
// doc right: Precision and Recall both 1.0.
func TestEvaluateCleanlySeparableToySet(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{"casino": 50, "hello": 0},
		ham:  map[string]int{"casino": 0, "hello": 80},
		c:    BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	labeled := []LabeledDoc{
		{Tokens: []string{"casino"}, Spam: true},
		{Tokens: []string{"casino"}, Spam: true},
		{Tokens: []string{"hello"}, Spam: false},
		{Tokens: []string{"hello"}, Spam: false},
	}

	m := Evaluate(f, "global", labeled, 1000, 0.0)

	if m.TP != 2 || m.FN != 0 {
		t.Errorf("spam docs: want TP=2 FN=0, got TP=%d FN=%d", m.TP, m.FN)
	}
	if m.TN != 2 || m.FP != 0 {
		t.Errorf("ham docs: want TN=2 FP=0, got TN=%d FP=%d", m.TN, m.FP)
	}
	if m.Precision != 1.0 {
		t.Errorf("Precision: want 1.0, got %f", m.Precision)
	}
	if m.Recall != 1.0 {
		t.Errorf("Recall: want 1.0, got %f", m.Recall)
	}
}

// TestEvaluateEmptySetNoDivideByZero guards the Precision/Recall
// denominator-zero cases: with no labeled docs at all, both metrics must
// be defined as 0.0 rather than NaN.
func TestEvaluateEmptySetNoDivideByZero(t *testing.T) {
	f := fakeBayes{c: BayesCounts{}}
	m := Evaluate(f, "global", nil, 1000, 0.0)
	if m.Precision != 0.0 || m.Recall != 0.0 {
		t.Errorf("empty set: want Precision=0.0 Recall=0.0, got Precision=%f Recall=%f", m.Precision, m.Recall)
	}
	if m.TP != 0 || m.FP != 0 || m.TN != 0 || m.FN != 0 {
		t.Errorf("empty set: want all-zero confusion matrix, got %+v", m)
	}
}

// TestEvaluateAllPredictedHamNoFalsePositives covers the Precision
// denominator-zero case specifically (TP+FP==0 while FN>0): when nothing
// is predicted spam, Precision must be 0.0, not NaN, even though Recall
// is also 0.0 for the same reason.
func TestEvaluateAllPredictedHamNoFalsePositives(t *testing.T) {
	f := fakeBayes{
		spam: map[string]int{"hello": 0},
		ham:  map[string]int{"hello": 80},
		c:    BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	labeled := []LabeledDoc{
		{Tokens: []string{"hello"}, Spam: true}, // mislabeled: ham-leaning tokens, but marked Spam
	}
	m := Evaluate(f, "global", labeled, 1000, 0.0)
	if m.TP != 0 || m.FP != 0 || m.FN != 1 {
		t.Errorf("want TP=0 FP=0 FN=1, got TP=%d FP=%d FN=%d", m.TP, m.FP, m.FN)
	}
	if m.Precision != 0.0 {
		t.Errorf("Precision: want 0.0 (0/0 guarded), got %f", m.Precision)
	}
	if m.Recall != 0.0 {
		t.Errorf("Recall: want 0.0, got %f", m.Recall)
	}
}
