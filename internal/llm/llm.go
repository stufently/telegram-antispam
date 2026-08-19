// Package llm implements the optional borderline-only LLM adjudication stage
// (spec §5.4). It runs one or two paid, official LLM APIs over a message whose
// Bayes score sits near the threshold and combines their verdicts under a
// consensus policy. It is entirely opt-in (config-gated) because it sends
// message text to an external service (spec §9 privacy rules).
//
// Fail-open by design: any provider or transport error errs toward "not spam"
// so an API outage, quota exhaustion, or timeout can never cause a ban.
package llm

import (
	"context"
	"strings"
)

// Provider is one LLM backend that classifies a single message as spam or not.
type Provider interface {
	// Name identifies the provider for logs/metrics (e.g. "openai").
	Name() string
	// Classify reports whether text is spam. A non-nil error means the call
	// failed and the result must be treated as "no opinion" by the Judge.
	Classify(ctx context.Context, text string) (bool, error)
}

// Policy is how the Judge combines multiple providers' verdicts.
type Policy string

const (
	// PolicyAny flags spam when at least one provider (that answered) says spam.
	PolicyAny Policy = "any"
	// PolicyAll flags spam only when every configured provider answered
	// successfully and all of them said spam. Any error breaks unanimity and
	// yields "not spam".
	PolicyAll Policy = "all"
)

// Judge runs the configured providers concurrently and reduces their answers
// under Policy. With a single provider it is a plain single call.
type Judge struct {
	Providers []Provider
	Policy    Policy
}

// result pairs a provider's spam verdict with whether the call succeeded.
type result struct {
	spam bool
	ok   bool
}

// Adjudicate returns true only when the consensus policy is satisfied. It is
// fail-open: with no providers, or when errors prevent the policy from being
// met, it returns false. Providers run concurrently; Adjudicate returns once
// all have answered or ctx is done.
func (j Judge) Adjudicate(ctx context.Context, text string) bool {
	if len(j.Providers) == 0 {
		return false
	}
	results := make([]result, len(j.Providers))
	done := make(chan struct{}, len(j.Providers))
	for i, p := range j.Providers {
		go func(i int, p Provider) {
			spam, err := p.Classify(ctx, text)
			results[i] = result{spam: spam, ok: err == nil}
			done <- struct{}{}
		}(i, p)
	}
	for range j.Providers {
		select {
		case <-ctx.Done():
			return false // fail-open: cancelled/timed out before consensus
		case <-done:
		}
	}
	return decide(j.Policy, results)
}

// decide applies the consensus policy to collected results. PolicyAll (the
// default for an unknown value) demands every provider succeed and agree on
// spam; PolicyAny needs one successful spam vote.
func decide(policy Policy, results []result) bool {
	switch policy {
	case PolicyAny:
		for _, r := range results {
			if r.ok && r.spam {
				return true
			}
		}
		return false
	default: // PolicyAll and any unknown policy: safest is unanimity
		for _, r := range results {
			if !r.ok || !r.spam {
				return false
			}
		}
		return true
	}
}

// classifyReply maps a model's free-text reply to a spam boolean: spam iff the
// first word (case-insensitive, punctuation-trimmed) is "SPAM". Anything else —
// "HAM", an explanation, an empty reply — is treated as not spam (fail-open).
func classifyReply(reply string) bool {
	f := strings.Fields(strings.ToUpper(reply))
	if len(f) == 0 {
		return false
	}
	return strings.Trim(f[0], ".,:;!?\"'*") == "SPAM"
}

// promptOr returns the operator's prompt, falling back to the built-in one.
func promptOr(prompt string) string {
	if strings.TrimSpace(prompt) != "" {
		return prompt
	}
	return classifyPrompt
}

const classifyPrompt = "You are a spam classifier for a Telegram group chat. " +
	"Decide whether the following message is spam (scam, ad, flood, phishing, or unsolicited promotion). " +
	"Reply with exactly one word: SPAM or HAM."
