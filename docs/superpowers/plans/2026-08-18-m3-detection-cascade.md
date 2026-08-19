# telegram-antispam M3 — Detection Cascade — Implementation Plan

> **Status: ✅ Implemented, reviewed, and merged to `main`.** Every step below is complete; the whole-branch review passed. Checkboxes are ticked for historical record.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Replace the M2 `decide` hook with a real, flat, pure-function detection cascade — normalize → trust-gate → hard rules → behavioral checks → verdict — so the bot actually decides what to act on, still defaulting new chats to dry-run.

**Architecture:** Detection is flat pure functions over one central normalized representation (spec §5). The normalizer collapses every text surface (text, caption, entities incl. hidden links and custom emoji, external_reply, sender_tag, poll options) and de-obfuscates (homoglyphs, zero-width, intra-word spacing) so downstream checks never see raw noise. The cascade short-circuits on any hard hit; trust only skips the (M4/M5) expensive semantic stages, never the cheap ones. The whole cascade is wired as `Handler.decide`, and because that path is now live, this milestone also makes the sequencer submit non-blocking (the M2 backpressure finding).

**Tech Stack:** Go 1.24+, `golang.org/x/text` (unicode normalization/runenames for confusables), stdlib. All build/test in Docker (`golang:1.26`) via `./scripts/dev.sh`.

**Spec:** `docs/superpowers/specs/2026-08-18-telegram-antispam-design.md` (implements build-order item 6 of spec §15, plus the M2-deferred sequencer-backpressure item).

## Global Constraints

- **Module path:** `github.com/stufently/telegram-antispam` (verbatim in every import).
- **No host installs.** Every `go` command runs in `golang:1.26` via `./scripts/dev.sh`. Never run `go` on the host.
- **Detection is pure (spec §3.1):** `internal/detect` imports only `internal/domain` (+ stdlib / x/text). No side effects, no Telegram library types, no store. The store-backed pieces (trust counter, recent-message history) live behind small interfaces the cascade receives, so the cascade stays unit-testable with fakes.
- **Verdict rule (spec §5):** `hard_hit OR bayes>=threshold` — M3 has no Bayes yet, so M3's verdict = hard hits + behavioral hits only; borderline stays "no action". Never fabricate a Bayes score.
- **Normalizer is central (spec §5.1):** every detector runs over the normalized representation, never the raw message. New payload surfaces are normalizer changes first.
- **Trust-gate (spec §5.2):** trusted after N meaningful messages (config, default 5); trust NEVER grants immunity — hard rules, blocklists, duplicate/flood/edit checks apply to everyone; trust only gates the expensive semantic stages (which arrive in M4/M5, so in M3 trust affects nothing destructive yet but the gate is built and tested).
- **New chats start in dry-run** (spec §10) — unchanged from M2; M3 does not auto-promote.
- **Message identity `(chat_id, message_id)`; never a text hash for identity.** (Text hashes are fine for *duplicate detection*, a different concern.)
- **English only** in code, comments, identifiers, commit messages.
- **Commit style:** ≤50-char imperative subject, no Co-Authored-By, no signatures, one commit per task.
- **TDD:** failing test first, watch fail, minimal code, watch pass, commit.

## Interfaces carried from M1+M2 (already on main, do not redefine)

- `domain.Message{ChatID,MessageID,ThreadID,MediaGroupID,Sender,Text,Date,IsAutomaticForward,LinkedChatID}` (M3 Task 1 extends it), `Sender{Kind,UserID,SenderChatID,Username,DisplayName}`, `Verdict{Action,Scope,Confidence,Signals,Reason}`, `Signal{Name,Detail}`, `Action`/`SenderKind` enums.
- `detect.ClassifySender`/`ClassifyInput` (M1).
- `telegram.Handler` with `decide func(domain.Message)(domain.Verdict,bool)` set via `SetDecide`; `ToDomainMessage` (adapter); `Sequencer.Submit`.
- `store.*DB` with `users PRIMARY KEY(chat_id,user_id)` table (M1 schema).
- `config.Config` (M3 Task 8 adds a `Detection` sub-struct).

---

### Task 1: Extend the domain message + adapter for detection surfaces

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/telegram/adapter.go`
- Test: `internal/telegram/adapter_test.go` (add cases)

**Interfaces:**
- Produces on `domain`: new type `Entity struct { Type string; URL string; Offset, Length int }` and new `Message` fields: `Entities []Entity`, `SenderTag string`, `ExternalReplyText string`, `PollOptionTexts []string`, `EditDate int64`, `HasMedia bool`.
- `ToDomainMessage` populates them from the library message: map `m.Entities`/`m.CaptionEntities` (Type as string, `URL` for `text_link`), `m.SenderBusinessBot`?? no — `m.SenderTag`? confirm via `./scripts/dev.sh doc`; `m.ExternalReply` text; `m.Poll.Options` texts; `m.EditDate`; `HasMedia = m.Photo/Video/Document/... != nil`.

- [x] **Step 1: Write failing test** — extend `adapter_test.go`:
```go
func TestToDomainMessageExtractsEntities(t *testing.T) {
	m := &models.Message{
		ID:   5, Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7},
		Text: "click here",
		Entities: []models.MessageEntity{
			{Type: models.MessageEntityTypeTextLink, URL: "http://x.test", Offset: 6, Length: 4},
		},
	}
	got := ToDomainMessage(m)
	if len(got.Entities) != 1 || got.Entities[0].URL != "http://x.test" || got.Entities[0].Type != "text_link" {
		t.Fatalf("entities not extracted: %+v", got.Entities)
	}
}
```

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** — add the domain fields and extend the adapter. Confirm exact library field/enum names via `./scripts/dev.sh doc github.com/go-telegram/bot/models.Message` and `.MessageEntity`. Map entity `Type` to its snake string (`text_link`, `url`, `mention`, `custom_emoji`, …). For `SenderTag`/`ExternalReply`/`Poll`, use the fields that exist in v1.23.0; if a field is absent, leave the domain field zero and note it in the report.

- [x] **Step 4: Run test + existing adapter tests, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/domain internal/telegram
git commit -m "Add detection fields to domain message"
```

---

### Task 2: Obfuscation normalizer

**Files:**
- Create: `internal/detect/normalize.go`
- Test: `internal/detect/normalize_test.go`

**Interfaces:**
- Produces: `func Deobfuscate(s string) string` — lowercases; NFKC-normalizes; strips zero-width runes (`​`-`‍`, `﻿`); folds a curated confusables map (Cyrillic/Greek lookalikes → Latin, e.g. а→a, е→e, о→o, р→p, с→c, х→x, і→i, ѕ→s, α→a); collapses runs of whitespace/punctuation used to space out letters ("п и ш и" → "пиши" only when single-letter tokens; keep it conservative — collapse single spaces between single letters).
- `func Confusable(r rune) (rune, bool)` — the per-rune fold table.

- [x] **Step 1: Write failing test**
```go
func TestDeobfuscate(t *testing.T) {
	cases := map[string]string{
		"НYЖНЫ":         "нужны", // latin Y -> ... actually keep example ascii-safe
		"h​e​re": "here",
		"СRYPTO":        "crypto",
	}
	for in, want := range cases {
		if got := Deobfuscate(in); got != want {
			t.Errorf("Deobfuscate(%q)=%q want %q", in, got, want)
		}
	}
}
```
> Author note for the implementer: pick confusable examples that are unambiguous;
> adjust the expected values to match the exact fold table you implement, but the
> table MUST at minimum fold the common Cyrillic а/е/о/р/с/х and strip zero-width.

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** using `golang.org/x/text/unicode/norm` (add dep: `./scripts/dev.sh get golang.org/x/text@v0.21.0 && make tidy`) for NFKC, a `map[rune]rune` confusables table, a zero-width strip, and a conservative single-letter-spacing collapse. Keep the table small and documented.

- [x] **Step 4: Run test, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/detect go.mod go.sum
git commit -m "Add obfuscation normalizer"
```

---

### Task 3: Message flattener → NormalizedMessage

**Files:**
- Create: `internal/detect/normalize_message.go`
- Test: `internal/detect/normalize_message_test.go`

**Interfaces:**
- Consumes: `domain.Message`, `Deobfuscate` (Task 2).
- Produces:
  - `type NormalizedMessage struct { Text string; Links []string; Mentions []string; HasCustomEmoji bool; SenderTagNorm string; RawLen int }`.
  - `func Normalize(m domain.Message) NormalizedMessage` — concatenates text + caption + external-reply text + poll option texts + sender tag, runs `Deobfuscate` on the concatenation into `Text`; collects `Links` from `Entities` (`text_link` URLs) plus URL-looking tokens in the text (a simple `https?://` / `t.me/` / `@handle` scan); collects `@mentions`; sets `HasCustomEmoji` if any entity type is `custom_emoji`; `RawLen` is the rune length of the visible text before dedup.

- [x] **Step 1: Write failing test**
```go
func TestNormalizeCollectsLinksAndText(t *testing.T) {
	m := domain.Message{
		Text: "join now",
		Entities: []domain.Entity{{Type: "text_link", URL: "http://spam.test", Offset: 0, Length: 4}},
		PollOptionTexts: []string{"vote t.me/scam"},
	}
	n := Normalize(m)
	if !contains(n.Links, "http://spam.test") {
		t.Fatalf("hidden text_link not collected: %v", n.Links)
	}
	if !containsSubstr(n.Text, "join now") {
		t.Fatalf("text not normalized in: %q", n.Text)
	}
}
```
(add small `contains`/`containsSubstr` test helpers.)

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement `Normalize`.**

- [x] **Step 4: Run test, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/detect
git commit -m "Add message flattener to normalized form"
```

---

### Task 4: Trust-gate (store-backed counter behind an interface)

**Files:**
- Create: `internal/store/trust.go`
- Test: `internal/store/trust_test.go`
- Create: `internal/detect/trust.go`
- Test: `internal/detect/trust_test.go`

**Interfaces:**
- Produces on `store`: `func (db *DB) BumpTrust(chatID, userID int64) (count int, err error)` — increments a `meaningful_count` on the `users` row (adding the column via an idempotent `ALTER TABLE ... ADD COLUMN` guarded by a "column exists" check, or extend the M1 `users` schema in `migrate.go` — prefer extending `migrate.go`'s `users` CREATE with `meaningful_count INTEGER NOT NULL DEFAULT 0` since it's `IF NOT EXISTS` and fresh DBs are the norm; for existing DBs add a guarded ALTER in Migrate). `func (db *DB) TrustCount(chatID, userID int64) (int, error)`.
- Produces on `detect`: `type TrustSource interface { TrustCount(chatID, userID int64) (int, error) }`; `func IsTrusted(src TrustSource, chatID, userID int64, threshold int) bool` (true when count >= threshold). `func IsMeaningful(n NormalizedMessage) bool` — a message counts toward trust only if it has real content (e.g. `RawLen >= 3` and not just an emoji/"+"/sticker); pure.

- [x] **Step 1: Write failing tests** — `store/trust_test.go` (Bump increments, TrustCount reads) using `newMigrated`; `detect/trust_test.go` (IsTrusted threshold with a fake TrustSource; IsMeaningful rejects "+", accepts real text).

- [x] **Step 2: Run tests, expect failure.**

- [x] **Step 3: Implement** — extend `users` schema with `meaningful_count`, add `BumpTrust`/`TrustCount`, add `IsTrusted`/`IsMeaningful`.

- [x] **Step 4: Run tests, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/store internal/detect
git commit -m "Add trust gate and meaningful counter"
```

---

### Task 5: Hard rules (deny/allow lists, link policy)

**Files:**
- Create: `internal/detect/rules.go`
- Test: `internal/detect/rules_test.go`

**Interfaces:**
- Consumes: `NormalizedMessage`.
- Produces:
  - `type Rules struct { DenyStopwords []string; AllowStopwords []string; BlockLinksForUntrusted bool; BannedDomains []string }`.
  - `func (r Rules) Check(n NormalizedMessage, trusted bool) (domain.Signal, bool)` — returns a hard-hit signal + true when: the normalized text contains a deny stopword (and not an allow override), OR (`BlockLinksForUntrusted && !trusted && len(n.Links) > 0`), OR any link's host is in `BannedDomains`. Deterministic order; first match wins.

- [x] **Step 1: Write failing test** — deny stopword hits; allow overrides; untrusted+link hits when policy on; trusted+link does NOT hit under the link policy; banned domain hits.

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement `Rules.Check`** (substring match on normalized text for stopwords; host extraction for domains).

- [x] **Step 4: Run test, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/detect
git commit -m "Add hard rules deny allow and link policy"
```

---

### Task 6: Behavioral checks (duplicate flood, short-message flood, edits)

**Files:**
- Create: `internal/detect/behavior.go`
- Test: `internal/detect/behavior_test.go`

**Interfaces:**
- Produces:
  - `type History interface { RecordAndCountDup(chatID, userID int64, textHash string, window time.Duration) int; RecentShortCount(chatID, userID int64, window time.Duration) int }` — an in-memory sliding-window store the cascade receives (implemented in Task 7 as a concrete `MemHistory`).
  - `func DupHash(n NormalizedMessage) string` — a stable hash of the normalized text (sha256 hex of `n.Text`), used ONLY for duplicate detection, never identity.
  - `func CheckBehavior(h History, chatID, userID int64, n NormalizedMessage, edited bool, cfg BehaviorCfg) (domain.Signal, bool)` where `BehaviorCfg{DupThreshold int; DupWindow time.Duration; ShortLen int; ShortFloodThreshold int; ShortWindow time.Duration; FlagEdits bool}`. Hits when: same `DupHash` seen `>= DupThreshold` times in `DupWindow`; OR short messages (`RawLen <= ShortLen`) `>= ShortFloodThreshold` in `ShortWindow`; OR (`FlagEdits && edited`).

- [x] **Step 1: Write failing test** — with a fake `History`, assert dup-threshold hit, short-flood hit, edit hit; and NO hit below thresholds.

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement `DupHash` + `CheckBehavior`.**

- [x] **Step 4: Run test, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/detect
git commit -m "Add behavioral duplicate and flood checks"
```

---

### Task 7: In-memory sliding-window History

**Files:**
- Create: `internal/detect/memhistory.go`
- Test: `internal/detect/memhistory_test.go`

**Interfaces:**
- Consumes: the `History` interface (Task 6).
- Produces: `type MemHistory struct { ... }`, `func NewMemHistory() *MemHistory` implementing `History`; mutex-guarded; entries older than the window are pruned lazily on access; a `now func() time.Time` seam for deterministic tests. `func (h *MemHistory) Sweep(maxAge time.Duration)` to bound memory (called periodically by the wiring).

- [x] **Step 1: Write failing test** (fake clock): record the same hash 3× within window → count reflects it; advance clock past window → count resets; short-count similar.

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement `MemHistory`** with per-(chat,user) ring/slice of `{hash, ts, short}` events, pruned by window; race-safe.

- [x] **Step 4: Run test with -race, expect pass** — `./scripts/dev.sh test -race ./internal/detect/`.

- [x] **Step 5: Commit**
```bash
git add internal/detect
git commit -m "Add in-memory sliding window history"
```

---

### Task 8: Detection config + cascade assembly

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/detect/cascade.go`
- Test: `internal/detect/cascade_test.go`
- Test: `internal/config/config_test.go` (add case)

**Interfaces:**
- Produces on `config`: a `Detection` sub-struct on `Config` (`TrustThreshold int`, `Rules detect-shaped fields`, `Behavior` fields) with sane defaults applied in `Validate`/a `Defaults()` (e.g. TrustThreshold default 5, DupThreshold 3, ShortLen 10, etc.). Keep YAML keys documented in `config.example.yaml`.
- Produces on `detect`: `type Cascade struct { Trust TrustSource; Hist History; Rules Rules; Behavior BehaviorCfg; TrustThreshold int; DefaultAction domain.Action; DefaultScope domain.Scope }` and `func (c Cascade) Decide(m domain.Message, edited bool) (domain.Verdict, bool)`:
  1. `n := Normalize(m)`; if sender immune kinds are already filtered upstream, assume a real user here.
  2. `trusted := IsTrusted(c.Trust, m.ChatID, m.Sender.UserID, c.TrustThreshold)`.
  3. hard rules: `if sig, hit := c.Rules.Check(n, trusted); hit { return actionable(sig) , true }`.
  4. behavioral (everyone): `if sig, hit := c.CheckBehavior(...); hit { return actionable(sig), true }`.
  5. if the message is meaningful, `c.Trust.Bump...` — NO: bumping is a side effect; keep Decide pure and return a second bool/flag OR do the bump in the wiring (Task 9). Decide returns `(Verdict, actionable bool)` only.
  6. otherwise `return domain.Verdict{Action: ActionNone}, false`.
  - `actionable(sig)` builds `Verdict{Action: c.DefaultAction, Scope: c.DefaultScope, Confidence: 1.0, Signals: []Signal{sig}, Reason: sig.Name}`.

- [x] **Step 1: Write failing test** — a `Cascade` with a fake TrustSource + fake History + simple Rules: a deny-stopword message returns actionable; a benign short message from a trusted user returns not-actionable; an untrusted message with a link (link policy on) returns actionable.

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** the `Detection` config (+defaults) and `Cascade.Decide`.

- [x] **Step 4: Run tests, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/config internal/detect config.example.yaml
git commit -m "Add detection config and cascade"
```

---

### Task 9: Wire the cascade as decide + non-blocking sequencer submit

**Files:**
- Modify: `internal/telegram/bot.go`
- Modify: `internal/telegram/sequencer.go`
- Modify: `cmd/tg-antispam/main.go`
- Test: `internal/telegram/bot_wire_test.go` (add case); `internal/telegram/sequencer_test.go` (add case)

**Interfaces:**
- Produces:
  - `Sequencer.Submit` becomes non-blocking on a full queue: if a chat's buffer is full, drop the job and increment a dropped counter (`func (s *Sequencer) Dropped() int64`), logging once — this fixes the M2 finding that a full buffer could block the single poll goroutine. Existing ordering/tests unchanged for the non-full case.
  - Wiring in `main.go`: build a `detect.Cascade` from config (TrustSource = the store, History = a `MemHistory` swept periodically), and `handler.SetDecide(func(m domain.Message)(domain.Verdict,bool){ v, ok := cascade.Decide(m, false); return v, ok })`. For edited messages, pass `edited=true` (thread an `editedDecide` or a second hook). After a non-actionable meaningful message, bump the user's trust counter (side effect in the wiring, not in Decide) — do this inside the sequencer job so it's off the poll path.
  - Register a periodic `MemHistory.Sweep` on a ticker tied to ctx.

- [x] **Step 1: Write failing tests** — (a) `sequencer_test.go`: fill a chat's buffer to capacity+1 and assert `Submit` returns without blocking and `Dropped()` increments (use a tiny buffer via a test seam or fill 1024+ jobs that block on a gate). (b) `bot_wire_test.go`: set decide to the real cascade with a fake trust/history that makes a stopword message actionable, and assert the machine is driven (copy before ban), reusing the M2 pattern.

- [x] **Step 2: Run tests, expect failure.**

- [x] **Step 3: Implement** the non-blocking submit (`select { case q<-job: default: drop++ }`), the cascade wiring, the trust bump in the job, and the sweep ticker. Keep FIFO/ordering for the non-full path identical.

- [x] **Step 4: Run tests + build + vet + race, expect pass** — `make test && make build && make vet`; `./scripts/dev.sh test -race ./internal/telegram/`.

- [x] **Step 5: Commit**
```bash
git add internal/telegram cmd
git commit -m "Wire detection cascade and non-blocking submit"
```

---

## Milestone 3 Definition of Done

- `make test` (and `-race` on `detect`, `telegram`) green; `make build`/`make vet` clean.
- The normalizer collapses all text surfaces and de-obfuscates; every detector runs over it.
- Trust-gate counts meaningful messages and is consulted by the link policy; trust never bypasses hard/behavioral checks.
- Hard rules (stopwords, link policy, banned domains) and behavioral checks (duplicate flood, short flood, edits) produce hard-hit verdicts.
- The cascade is wired as `Handler.decide`; actionable verdicts drive the (M2) incident machine, evidence-first, still per-chat dry-run for new chats.
- `Sequencer.Submit` no longer blocks the poll goroutine on a full buffer (M2 finding closed).
- Nothing installed on host; all in `golang:1.26`.

**Next milestone (separate plan):** M4 — naive Bayes over normalized tokens + idempotent sample import + offline calibration, extending the cascade's borderline path (trusted users start being evaluated by Bayes; LLM stays out until M6).
