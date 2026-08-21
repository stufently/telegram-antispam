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
	// Classify reports whether text is spam, using prompt as the system
	// prompt when non-empty (the provider's own configured prompt, then the
	// built-in one, are the fallbacks). The prompt is a per-CALL argument
	// rather than provider state because different chats need different
	// wording and calls for several chats run concurrently — storing it on
	// the provider would be a data race with a wrong-prompt outcome.
	// A non-nil error means the call failed and the result must be treated
	// as "no opinion" by the Judge.
	Classify(ctx context.Context, text, prompt string) (bool, error)
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

// Outcome is the result of one adjudication: the verdict plus how the
// providers actually behaved while producing it.
//
// Failed exists because fail-open makes an outage look exactly like a "not
// spam" answer. With only a boolean returned, an expired API key, an
// exhausted quota or a timing-out endpoint reads at the call site as "the
// model said HAM" — and this stage is the only one that catches spam the
// Bayes corpus has never seen. The caller is expected to surface Failed so
// a dead paid stage is visible instead of silently degrading detection.
type Outcome struct {
	Spam   bool  // the consensus verdict (false when the policy is unmet)
	Failed int   // providers that errored, or never answered before ctx died
	Total  int   // providers asked
	Err    error // one representative failure, for logging
}

// Adjudicate returns the consensus verdict together with how many providers
// failed to answer. It is fail-open: with no providers, or when errors
// prevent the policy from being met, Spam is false. Providers run
// concurrently; Adjudicate returns once all have answered or ctx is done.
func (j Judge) Adjudicate(ctx context.Context, text, prompt string) Outcome {
	out := Outcome{Total: len(j.Providers)}
	if len(j.Providers) == 0 {
		return out
	}
	type answer struct {
		i   int
		res result
		err error
	}
	results := make([]result, len(j.Providers))
	done := make(chan answer, len(j.Providers))
	for i, p := range j.Providers {
		go func(i int, p Provider) {
			spam, err := p.Classify(ctx, text, prompt)
			done <- answer{i: i, res: result{spam: spam, ok: err == nil}, err: err}
		}(i, p)
	}
	answered := 0
	record := func(a answer) {
		answered++
		results[a.i] = a.res
		if a.err != nil {
			out.Failed++
			out.Err = a.err
		}
	}
	for answered < len(j.Providers) {
		// Answers that have ALREADY arrived are taken first, before the
		// deadline is honoured. A plain two-case select would pick at
		// random when both are ready, so a provider that answered just
		// before the timeout could be thrown away — turning a caught spam
		// message into fail-open ham on nothing but scheduling luck.
		select {
		case a := <-done:
			record(a)
			continue
		default:
		}
		select {
		case a := <-done:
			record(a)
		case <-ctx.Done():
			// Deadline reached with nothing further pending. Providers
			// still outstanding count as failures — on top of, not
			// instead of, the ones that already errored.
			out.Failed += len(j.Providers) - answered
			if out.Err == nil {
				out.Err = ctx.Err()
			}
			// Still apply the policy to what DID arrive. Under "any", a
			// provider that already answered spam is a spam verdict; the
			// slow one cannot retract it. Under "all", the unanswered slot
			// stays not-ok, so unanimity fails and the result is not-spam —
			// fail-open, as before.
			out.Spam = decide(j.Policy, results)
			return out
		}
	}
	out.Spam = decide(j.Policy, results)
	return out
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

// promptOr picks the first non-blank prompt: the per-call one (a chat
// override), then the provider's configured one, then the built-in default.
func promptOr(prompts ...string) string {
	for _, p := range prompts {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	return classifyPrompt
}

const classifyPrompt = "You are a spam classifier for a Telegram group chat. " +
	"Decide whether the following message is spam (scam, ad, flood, phishing, or unsolicited promotion). " +
	"Reply with exactly one word: SPAM or HAM."
