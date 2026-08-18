# telegram-antispam M5 — Newcomer & Impersonation Defenses — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the three library-grounded v1 platform features from spec §5.5 and §12 — fake-admin (impersonation) detection, spammer reaction cleanup, and ephemeral (per-user-visible) newcomer notices — so the bot catches the impersonation attack content-only bots miss, strips reactions left by known spammers, and can tell a sanctioned newcomer why without littering the chat.

**Architecture:** Fake-admin detection is a pure detector in `internal/detect` (bounded Levenshtein vs the chat's admin names/titles + a suspicious-tag check), fed the admin identity list through an injected `AdminSource` interface that the wiring backs with a short-TTL cache over `port.GetChatAdministrators` (spec §4). It runs as a cascade stage for non-trusted senders. A `chat_member`-update handler tracks each user's last-known identity in SQLite and raises an admin-chat notice when someone renames themselves to look like an admin after joining. Reaction cleanup adds a `DeleteMessageReaction` port method and a `message_reaction`-update handler that removes reactions added by a user with a prior spam incident in that chat (fail-open, config-gated). Ephemeral notices add a `SendEphemeral` port method and a best-effort hook in the incident machine that privately tells a sanctioned user their message was removed (never a verification gate — delivery is not guaranteed, spec §12).

**Tech Stack:** Go 1.24+, `github.com/go-telegram/bot` v1.23.0 (Bot API 10.2), `modernc.org/sqlite`, stdlib. All build/test in Docker (`golang:1.26`) via `./scripts/dev.sh`.

**Spec:** `docs/superpowers/specs/2026-08-18-telegram-antispam-design.md` (implements spec §5.5 and the "In v1" items of §12; build-order items 8–9, minus blocklists — see scope note). **Library API facts:** `.superpowers/m5-library-facts.md` (verified against the vendored library source).

## Scope note (ruling)

Spec build-order 8–9 bundles LOLS/CAS **blocklists** with these features. Blocklists depend on external-service APIs (LOLS delta export, CAS full export) whose endpoints and formats are not verifiable against the library and need their own research + syncer design. To keep each milestone shippable and correct, **blocklists are deferred to their own next milestone**; M5 delivers the three self-contained, library-grounded §12 v1 features. Where a feature would naturally consult a blocklist ("is this reactor a known spammer?"), M5 uses the already-built local signal (a prior spam incident / trust state) and the policy is written so a blocklist source slots in later without redesign.

## Global Constraints

- **Module path:** `github.com/stufently/telegram-antispam` (verbatim).
- **No host installs.** Every `go` command via `./scripts/dev.sh` in `golang:1.26`.
- **Detection stays pure (spec §3.1):** `internal/detect` imports only `domain` + stdlib + `x/text`. The admin identity list arrives via an `AdminSource` interface implemented in the wiring; the detector never calls the Telegram port or the store.
- **Only `internal/telegram` and `cmd` import the bot library** (spec §3.1). New port methods live behind the `telegram.Port` interface; `LivePort` wraps them through the existing `submitSync`/`submitSyncErr` + `mapRetry` dispatcher path (see `.superpowers/m5-library-facts.md` §5).
- **Single inline update consumer (spec §11 / M1):** the bot runs `WithNotAsyncHandlers()` + `WithWorkers(1)`; new `chat_member` / `message_reaction` handling must run on that one consumer (offloaded to the existing `seq` sequencer for network/DB work, exactly like the callback path), never spawn concurrent update processing.
- **Admins are immune (spec §4):** the moderation pipeline never runs on an admin's message, so any message reaching the fake-admin stage is from a non-admin — a matching admin name/tag there is impersonation, not a false positive.
- **`SenderTag` is user-controlled (library facts §4):** `Message.SenderTag` / `ChatMember.Tag` is member-editable, so it is NEVER trusted as proof of admin status — a member *claiming* an admin-like tag is a spam **signal**, and admin ground truth always comes from `GetChatAdministrators` + `CustomTitle`.
- **Fail-open / best-effort:** reaction cleanup and ephemeral notices never block moderation and never fail an incident — a reaction-delete error or an undelivered ephemeral is logged and swallowed (spec §12: ephemeral is never the sole path).
- **Config explicit-0/false semantics (M3/M4 convention):** new bool toggles are `*bool` (nil = unset → default) so an explicit `false` is honored; feature toggles default per the task text.
- **English only**; commit ≤50-char imperative, no trailers, one commit per task; TDD.

## Interfaces carried from M1–M4 (on main, do not redefine)

- `domain`: `Message{ChatID,MessageID,Sender,Text,SenderTag,Entities,...}`, `Sender{Kind,UserID,SenderChatID,Username,DisplayName}`, `Verdict{Action,Scope,Confidence,Signals,Reason}`, `Signal{Name,Detail}`, `Action`/`Scope`, `Incident{ChatID,MessageIDs,Sender,Verdict,...}`.
- `detect`: `Normalize(m)->NormalizedMessage`, `Cascade{Trust,Hist,Rules,Behavior,TrustThreshold,DefaultAction,DefaultScope,Bayes,BayesScope,BayesThreshold,BayesVocabGuess,BayesEnabled}` + `Decide(m,edited)`, `IsTrusted(src,chat,user,threshold)`, `TrustSource`, `BayesSource`. `Decide` order today: Rules → Behavior → Bayes.
- `store`: `*DB` (Open/Migrate/Write/Read), `addColumnIfMissing`, tables incl. `incidents(id,chat_id,message_id,user_id,state,dry_run,created_at)`, `users(chat_id,user_id,meaningful_count)`. Migrations are idempotent `CREATE TABLE IF NOT EXISTS` in `internal/store/migrate.go`.
- `telegram`: `Port` interface (CopyMessages, DeleteMessages, BanMember, RestrictMember, SendAdmin, BanSenderChat, GetChatAdministrators, AnswerCallback, EditAdminMarkup), `LivePort` (`submitSync[T]`/`submitSyncErr`/`mapRetry`/`prio`), narrow types `Member{UserID,Status,Username,DisplayName}`, `AdminMessage`, `Button`, `Perms{CanSend bool}`. Fake port for tests in `internal/telegram/fake`.
- `incident`: `Machine{port,repo,adminChatID,buttonsFor}`, `Handle(ctx,inc)`, `applyAction(ctx,inc)` (switch on `inc.Verdict.Action`: Ban→BanMember, Mute/DeleteMute→RestrictMember, else nothing), then `DeleteMessages`.
- `config`: `Config{BotToken,AdminChatID,Action,Chats,Detection}`; `Detection` fields via `applyDetectionDefaults`. `config.example.yaml` documents keys.
- `cmd/tg-antispam/main.go`: `WithAllowedUpdates([...,"chat_member","my_chat_member","message_reaction"])` is ALREADY set; the `WithDefaultHandler` switch handles only `Message`/`EditedMessage`/`CallbackQuery` today. `seq.Submit(bucket, func(){...})` offloads network/DB work off the poll consumer.

---

### Task 1: Bounded Levenshtein distance

**Files:**
- Create: `internal/detect/levenshtein.go`
- Test: `internal/detect/levenshtein_test.go`

**Interfaces:**
- Produces: `func LevenshteinWithin(a, b string, max int) bool` — reports whether the edit distance between `a` and `b` is ≤ `max`, comparing over runes (not bytes) so Unicode names compare correctly. Early-exits: if `abs(len(a)-len(b)) > max` returns false immediately; uses the two-row DP but bails to false as soon as every cell in a row exceeds `max`. Pure.

- [ ] **Step 1: Write the failing test**

```go
package detect

import "testing"

func TestLevenshteinWithin(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want bool
	}{
		{"admin", "admin", 1, true},   // identical
		{"admin", "admln", 1, true},   // 1 substitution
		{"admin", "admins", 1, true},  // 1 insertion
		{"admin", "amin", 1, true},    // 1 deletion
		{"admin", "aXmiY", 1, false},  // 2 edits
		{"admin", "support", 1, false},
		{"Аdmin", "Admin", 1, true}, // cyrillic А vs latin A = 1 sub over runes
		{"", "", 1, true},
		{"a", "", 1, true},
		{"abc", "", 1, false}, // 3 deletions
	}
	for _, c := range cases {
		if got := LevenshteinWithin(c.a, c.b, c.max); got != c.want {
			t.Errorf("LevenshteinWithin(%q,%q,%d)=%v want %v", c.a, c.b, c.max, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test, expect failure** — `./scripts/dev.sh test ./internal/detect/... -run TestLevenshteinWithin` → undefined: LevenshteinWithin.

- [ ] **Step 3: Implement** rune-based two-row DP with the length-difference early-exit and a per-row "min exceeds max ⇒ return false" bail. Convert both strings to `[]rune` once.

- [ ] **Step 4: Run test, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/detect/levenshtein.go internal/detect/levenshtein_test.go
git commit -m "Add bounded Levenshtein distance"
```

---

### Task 2: Fake-admin pure detector

**Files:**
- Create: `internal/detect/fakeadmin.go`
- Test: `internal/detect/fakeadmin_test.go`

**Interfaces:**
- Consumes: `LevenshteinWithin` (Task 1); `domain.Message`, `domain.Signal`; `Normalize`/`NormalizedMessage` for casefolding.
- Produces:
  - `type AdminIdentity struct { Username, DisplayName, CustomTitle string }` — one current admin's public identifiers.
  - `type AdminSource interface { AdminIdentities(chatID int64) []AdminIdentity }` — injected; returns the cached admin list for a chat (empty slice if unknown/uncached — the detector treats empty as "no basis", never panics).
  - `type FakeAdminCfg struct { Enabled bool; SuspiciousTags []string; MaxDistance int }` — `MaxDistance` is the Levenshtein bound (default 1 supplied by caller/config).
  - `func CheckFakeAdmin(m domain.Message, admins []AdminIdentity, cfg FakeAdminCfg) (domain.Signal, bool)` — pure. Returns an actionable `Signal{Name:"fake_admin", Detail:...}` when, for a NON-trusted non-admin sender (caller guarantees this): (a) the sender's normalized `Username` or `DisplayName` is within `cfg.MaxDistance` of any admin's `Username`, `DisplayName`, or `CustomTitle` **but not exactly equal to their own legitimate value** (an exact, identical string to a DIFFERENT admin is still impersonation — see below); OR (b) the message's normalized `SenderTag` casefold-equals any entry in `cfg.SuspiciousTags`. Comparisons are casefolded via `strings.ToLower` over the normalized form; empty admin fields and an empty sender field never match (guard `len==0`). If `!cfg.Enabled` or `len(admins)==0` and no suspicious tag configured-hit, returns `(Signal{}, false)`.

  Impersonation vs. self: the detector is only ever called for a sender that is NOT an admin of this chat (spec §4 immunity — caller guarantees). Therefore ANY near-match to an admin identity is suspicious, including an exact copy. Do not try to exclude "the sender's own name" — a non-admin whose name exactly equals an admin's name is the textbook impersonation case.

- [ ] **Step 1: Write the failing test**

```go
package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestCheckFakeAdmin(t *testing.T) {
	admins := []AdminIdentity{{Username: "realadmin", DisplayName: "Group Owner", CustomTitle: "Founder"}}
	cfg := FakeAdminCfg{Enabled: true, SuspiciousTags: []string{"admin", "support"}, MaxDistance: 1}

	// Near-match username (1 edit) → flagged.
	m := domain.Message{Sender: domain.Sender{Username: "realadm1n", DisplayName: "nobody"}}
	if sig, hit := CheckFakeAdmin(m, admins, cfg); !hit || sig.Name != "fake_admin" {
		t.Fatalf("near-match username should flag: hit=%v sig=%+v", hit, sig)
	}

	// Suspicious sender tag on a plain user → flagged.
	m2 := domain.Message{Sender: domain.Sender{Username: "randomguy", DisplayName: "Random"}, SenderTag: "Admin"}
	if _, hit := CheckFakeAdmin(m2, admins, cfg); !hit {
		t.Fatal("suspicious sender tag should flag")
	}

	// Unrelated user, benign tag → not flagged.
	m3 := domain.Message{Sender: domain.Sender{Username: "alice", DisplayName: "Alice"}, SenderTag: "Member"}
	if _, hit := CheckFakeAdmin(m3, admins, cfg); hit {
		t.Fatal("unrelated user must not flag")
	}

	// Disabled → never flags.
	if _, hit := CheckFakeAdmin(m, admins, FakeAdminCfg{Enabled: false, MaxDistance: 1}); hit {
		t.Fatal("disabled must not flag")
	}

	// No admins known + benign tag → no basis, no flag.
	if _, hit := CheckFakeAdmin(m, nil, cfg); hit {
		t.Fatal("no admin list + no suspicious tag must not flag")
	}
}
```

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `AdminIdentity`, `AdminSource`, `FakeAdminCfg`, and `CheckFakeAdmin` per the interface. Casefold with `strings.ToLower`; guard empty fields; check username/display against each admin field via `LevenshteinWithin`; check `SenderTag` against `SuspiciousTags` by casefold equality.

- [ ] **Step 4: Run test, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/detect/fakeadmin.go internal/detect/fakeadmin_test.go
git commit -m "Add fake-admin pure detector"
```

---

### Task 3: Wire fake-admin into the cascade

**Files:**
- Modify: `internal/detect/cascade.go`
- Test: `internal/detect/cascade_test.go` (add cases)

**Interfaces:**
- Consumes: `CheckFakeAdmin`, `AdminSource`, `FakeAdminCfg` (Task 2).
- Produces: `Cascade` gains `Admins AdminSource`, `FakeAdmin FakeAdminCfg`. In `Decide`, add a stage that runs for NON-trusted senders, AFTER `Rules.Check` and BEFORE `CheckBehavior` (fake-admin is a hard, cheap identity signal — it should fire ahead of behavioral/Bayes): if `c.FakeAdmin.Enabled && !trusted`, call `admins := nil; if c.Admins != nil { admins = c.Admins.AdminIdentities(m.ChatID) }`, then `if sig, hit := CheckFakeAdmin(m, admins, c.FakeAdmin); hit { return c.actionable(sig), true }`. Trusted senders skip it (they have a track record). A nil `Admins` or empty list still lets the suspicious-tag check fire inside `CheckFakeAdmin`.

- [ ] **Step 1: Write the failing test** — extend `cascade_test.go`: a `Cascade` with untrusted sender, empty Rules, a fake `AdminSource` returning one admin `{Username:"owner"}`, `FakeAdmin:{Enabled:true,MaxDistance:1}`, and a message from sender `{Username:"0wner"}` ⇒ `Decide` returns actionable with `Reason=="fake_admin"`. A TRUSTED sender with the same message ⇒ not actionable (skipped).

```go
type fakeAdminSrc struct{ a []AdminIdentity }

func (f fakeAdminSrc) AdminIdentities(int64) []AdminIdentity { return f.a }

func TestCascadeDecide_FakeAdminUntrustedOnly(t *testing.T) {
	c := Cascade{
		Trust:         fakeTrust{trusted: false}, // reuse existing test double
		Hist:          fakeHist{},
		Admins:        fakeAdminSrc{a: []AdminIdentity{{Username: "owner"}}},
		FakeAdmin:     FakeAdminCfg{Enabled: true, MaxDistance: 1},
		DefaultAction: domain.ActionDeleteMute,
		DefaultScope:  domain.ScopeGlobal,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 2, Username: "0wner"}}
	if v, ok := c.Decide(m, false); !ok || v.Reason != "fake_admin" {
		t.Fatalf("untrusted fake-admin should flag: ok=%v v=%+v", ok, v)
	}
	c.Trust = fakeTrust{trusted: true}
	if _, ok := c.Decide(m, false); ok {
		t.Fatal("trusted sender must skip fake-admin")
	}
}
```
(Match the exact names/shape of the existing `fakeTrust`/`fakeHist` doubles already in `cascade_test.go`; adjust if they differ.)

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the stage in `Decide` in the specified order and add the two struct fields.

- [ ] **Step 4: Run tests + vet, expect pass** — confirm existing cascade tests (Bayes, rules, behavior) still pass; the new stage must not change their outcomes (they use `FakeAdmin.Enabled==false` zero value).

- [ ] **Step 5: Commit**
```bash
git add internal/detect/cascade.go internal/detect/cascade_test.go
git commit -m "Run fake-admin stage in cascade"
```

---

### Task 4: Admin-identity store (last-known name + admin-notice dedup)

**Files:**
- Modify: `internal/store/migrate.go`
- Create: `internal/store/identity.go`
- Test: `internal/store/identity_test.go`

**Interfaces:**
- Produces:
  - New idempotent table in `migrate.go`:
    ```sql
    CREATE TABLE IF NOT EXISTS user_identity (
        chat_id     INTEGER NOT NULL,
        user_id     INTEGER NOT NULL,
        username    TEXT    NOT NULL DEFAULT '',
        display_name TEXT   NOT NULL DEFAULT '',
        updated_at  INTEGER NOT NULL DEFAULT (strftime('%s','now')),
        PRIMARY KEY(chat_id, user_id)
    );
    ```
  - `func (db *DB) UpsertIdentity(chatID, userID int64, username, displayName string) (prevUsername, prevDisplay string, changed bool, err error)` — returns the PREVIOUS stored values (empty strings if the row is new) and `changed=true` when the row existed and either field differs from the incoming values; then upserts to the new values with `updated_at` bumped. One `db.Write` transaction: SELECT existing, then INSERT…ON CONFLICT DO UPDATE. A brand-new row returns `changed=false` (nothing to compare against).

- [ ] **Step 1: Write the failing test** — open a temp DB, `Migrate`; first `UpsertIdentity(1,2,"a","A")` ⇒ `changed=false`, prev empty; second `UpsertIdentity(1,2,"a","A")` (same) ⇒ `changed=false`; third `UpsertIdentity(1,2,"owner","Owner")` ⇒ `changed=true`, `prevUsername=="a"`, `prevDisplay=="A"`.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the table + `UpsertIdentity` (SELECT-then-upsert in one `db.Write`).

- [ ] **Step 4: Run tests (incl. `-race`), expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/store/migrate.go internal/store/identity.go internal/store/identity_test.go
git commit -m "Add user identity store"
```

---

### Task 5: chat_member identity tracking + impersonation notice

**Files:**
- Create: `internal/watch/member.go` (new small package `watch` for update-driven, non-message side-effects)
- Test: `internal/watch/member_test.go`

**Interfaces:**
- Consumes: `store.UpsertIdentity` (Task 4); `detect.LevenshteinWithin` + `detect.AdminIdentity`/`AdminSource` (Tasks 1–2); `telegram.Port.SendAdmin` for the notice.
- Produces:
  - `type IdentityStore interface { UpsertIdentity(chatID, userID int64, username, displayName string) (string, string, bool, error) }` (consumer-declared; `*store.DB` satisfies it).
  - `type MemberEvent struct { ChatID, UserID int64; Username, DisplayName string }` — the narrow, library-free shape the wiring extracts from `ChatMemberUpdated`.
  - `type MemberWatcher struct { Store IdentityStore; Admins detect.AdminSource; AdminChatID int64; Port telegram.Port; MaxDistance int; Enabled bool }`.
  - `func (w *MemberWatcher) Observe(ctx context.Context, e MemberEvent) error` — records the identity via `UpsertIdentity`; if `w.Enabled`, the row CHANGED, and the NEW name is within `w.MaxDistance` of some admin identity (and the user is not themselves an admin — check by id against the admin list if ids are available; otherwise name-match alone), send a best-effort admin-chat notice via `w.Port.SendAdmin` (`AdminMessage{Text: "possible admin impersonation: user <id> renamed to <name> (matches admin <adminname>)", SourceChatID: e.ChatID}`, no buttons, no copied evidence). A `SendAdmin` error is logged by the caller, not returned as fatal — `Observe` returns the store error if any, else nil.

- [ ] **Step 1: Write the failing test** — a fake `IdentityStore` scripted to return `changed=true` with a new name matching a fake `AdminSource`'s admin; a fake `telegram.Port` (reuse `internal/telegram/fake`) recording `SendAdmin`. `Observe` with an admin-like rename ⇒ exactly one `SendAdmin` call. A benign rename (no admin match) ⇒ zero `SendAdmin` calls. `Enabled:false` ⇒ zero calls even on a match.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `MemberWatcher.Observe` per interface. Casefold before Levenshtein; guard empty names.

- [ ] **Step 4: Run tests, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/watch/member.go internal/watch/member_test.go
git commit -m "Add member impersonation watcher"
```

---

### Task 6: Port method — DeleteMessageReaction

**Files:**
- Modify: `internal/telegram/port.go` (add to `Port` interface)
- Modify: `internal/telegram/livept.go` (implement)
- Modify: `internal/telegram/fake/fake.go` (record calls)
- Test: `internal/telegram/livept_test.go` or the fake's test (add a case)

**Interfaces:**
- Produces: `Port` gains `DeleteMessageReaction(ctx context.Context, chat int64, messageID int, userID int64) error`. `LivePort` implements it via `submitSyncErr(ctx, p.disp, chat, p.prio("DeleteMessageReaction"), func(ctx) error { _, err := p.b.DeleteMessageReaction(ctx, &bot.DeleteMessageReactionParams{ChatID: chat, MessageID: messageID, UserID: userID}); return mapRetry(err) })` (see library facts §1 for the exact param struct). The `fake` port records `(chat,messageID,userID)` tuples for assertions.

- [ ] **Step 1: Write the failing test** — extend the fake-port test (or add one) asserting the fake records a `DeleteMessageReaction` call; and add `var _ telegram.Port = (*fake.Port)(nil)` / `(*LivePort)(nil)` still compiles with the new method.

- [ ] **Step 2: Run test, expect failure** (interface not satisfied / method undefined).

- [ ] **Step 3: Implement** the interface method, the `LivePort` wrapper, and the `fake` recorder. Add `"DeleteMessageReaction"` to whatever priority map `p.prio` reads (mirror an existing low-priority entry).

- [ ] **Step 4: Run tests + build, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/telegram
git commit -m "Add DeleteMessageReaction port method"
```

---

### Task 7: Store query — known-spammer check

**Files:**
- Create: `internal/store/spammer.go`
- Test: `internal/store/spammer_test.go`

**Interfaces:**
- Produces: `func (db *DB) HasSpamIncident(chatID, userID int64) (bool, error)` — true when there is at least one `incidents` row for `(chat_id, user_id)` that is NOT a dry-run (`dry_run = 0`) and whose `state` reflects an applied action (`state IN ('acted','cleaned','done')`). Read-only (`db.Read`). This is the M5 "known spammer" signal; a blocklist source can be OR-ed in later without changing callers.

- [ ] **Step 1: Write the failing test** — insert (via existing incident-insert path or a direct helper) a non-dry-run acted incident for `(1,2)`; `HasSpamIncident(1,2)` ⇒ true; `HasSpamIncident(1,3)` ⇒ false; a dry-run incident for `(1,4)` ⇒ false.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the query. If the exact insert helper isn't available in tests, insert rows with a raw `db.Write` in the test's arrange step.

- [ ] **Step 4: Run tests, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/store/spammer.go internal/store/spammer_test.go
git commit -m "Add known-spammer incident query"
```

---

### Task 8: Reaction-cleanup handler

**Files:**
- Create: `internal/watch/reaction.go`
- Test: `internal/watch/reaction_test.go`

**Interfaces:**
- Consumes: `telegram.Port.DeleteMessageReaction` (Task 6); `store.HasSpamIncident` (Task 7).
- Produces:
  - `type SpammerSource interface { HasSpamIncident(chatID, userID int64) (bool, error) }` (consumer-declared; `*store.DB` satisfies it).
  - `type ReactionEvent struct { ChatID int64; MessageID int; UserID int64; Added bool }` — narrow shape; `Added` is true when `len(NewReaction) > len(OldReaction)` (the user placed/added a reaction, not removed one).
  - `type ReactionCleaner struct { Spammers SpammerSource; Port telegram.Port; Enabled bool }`.
  - `func (r *ReactionCleaner) Observe(ctx context.Context, e ReactionEvent) error` — no-op unless `r.Enabled && e.Added && e.UserID != 0`. Then `known, err := r.Spammers.HasSpamIncident(e.ChatID, e.UserID)`; on `err` return it (caller logs, fail-open — the update is dropped, chat not blocked); if `known`, call `r.Port.DeleteMessageReaction(ctx, e.ChatID, e.MessageID, e.UserID)` and return its error (caller logs). A non-spammer reaction is left untouched.

- [ ] **Step 1: Write the failing test** — fake `SpammerSource` (returns true for user 2), fake port recording `DeleteMessageReaction`. `Observe` with `{ChatID:1,MessageID:9,UserID:2,Added:true}` ⇒ one delete call for `(1,9,2)`. Same event for user 3 (not a spammer) ⇒ zero deletes. `Added:false` (reaction removed) ⇒ zero deletes. `Enabled:false` ⇒ zero deletes.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `ReactionCleaner.Observe` per interface.

- [ ] **Step 4: Run tests, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/watch/reaction.go internal/watch/reaction_test.go
git commit -m "Add spammer reaction cleanup handler"
```

---

### Task 9: Port method — SendEphemeral

**Files:**
- Modify: `internal/telegram/port.go` (add to `Port` interface)
- Modify: `internal/telegram/livept.go` (implement)
- Modify: `internal/telegram/fake/fake.go` (record calls)
- Test: fake/livept test (add a case)

**Interfaces:**
- Produces: `Port` gains `SendEphemeral(ctx context.Context, chat, userID int64, text string) (int, error)` — returns the library's `EphemeralMessageID` (spec §12 / library facts §2). `LivePort` implements via `submitSync[int](ctx, p.disp, chat, p.prio("SendEphemeral"), func(ctx) (int, error) { msg, err := p.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, ReceiverUserID: userID, Text: text}); if err != nil { return 0, mapRetry(err) }; return msg.EphemeralMessageID, nil })` (adapt to the actual `submitSync` signature/generics in `livept.go`). The `fake` port records `(chat,userID,text)` and returns a canned id.

- [ ] **Step 1: Write the failing test** — fake port records a `SendEphemeral` call and returns a fixed id; interface assertions still compile.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** interface method + `LivePort` wrapper + `fake` recorder + `p.prio` entry.

- [ ] **Step 4: Run tests + build, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/telegram
git commit -m "Add SendEphemeral port method"
```

---

### Task 10: Ephemeral notice hook in the incident machine

**Files:**
- Modify: `internal/incident/machine.go`
- Test: `internal/incident/machine_test.go` (add a case)

**Interfaces:**
- Consumes: `telegram.Port.SendEphemeral` (Task 9).
- Produces: `Machine` gains `EphemeralNotice bool` and `EphemeralText string` (installed by the wiring; defaults off/empty). After `applyAction` runs for an actionable, NON-dry-run incident whose action restricts/removes the user (`ActionMute`, `ActionDeleteMute`, `ActionBan`, `ActionDeleteOnly`, `ActionQuarantine`), if `m.EphemeralNotice && m.EphemeralText != "" && inc.Sender.UserID != 0`, call `m.port.SendEphemeral(ctx, inc.ChatID, inc.Sender.UserID, m.EphemeralText)` **best-effort**: an error is logged (or, since the machine has no logger, swallowed with a `_ =`) and never fails `Handle`. It runs AFTER the sanction and evidence steps so a delivery hiccup can't delay moderation, and only in live (non-dry-run) mode.

- [ ] **Step 1: Write the failing test** — a `Machine` with a fake port, `EphemeralNotice:true`, `EphemeralText:"removed pending review"`, handling a non-dry-run `ActionDeleteMute` incident for user 5 ⇒ exactly one `SendEphemeral(chat,5,"removed pending review")` after the restrict+delete. A dry-run incident ⇒ zero `SendEphemeral`. `EphemeralNotice:false` ⇒ zero. A `SendEphemeral` error ⇒ `Handle` still returns nil (best-effort).

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the hook per interface; ensure ordering (after existing action/delete) and the dry-run + enable + non-empty-text + non-zero-user guards.

- [ ] **Step 4: Run tests, expect pass** — confirm existing machine tests (evidence-before-action, reprocess guard) are unaffected.

- [ ] **Step 5: Commit**
```bash
git add internal/incident/machine.go internal/incident/machine_test.go
git commit -m "Send ephemeral notice on sanction"
```

---

### Task 11: Config — fake-admin, reaction cleanup, ephemeral notice

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`
- Test: `internal/config/config_test.go` (add cases)

**Interfaces:**
- Produces on `Config.Detection` (and a new top-level `Features` block as fits the existing shape — follow whatever nesting `Detection` uses):
  - `FakeAdminEnabled *bool` (yaml `fake_admin_enabled`, default true), `FakeAdminMaxDistance int` (yaml `fake_admin_max_distance`, default 1, zero treated as unset→1), `FakeAdminSuspiciousTags []string` (yaml `fake_admin_suspicious_tags`, default `["admin","support","verified","moderator"]` when nil — nil means unset; an explicit empty list in YAML disables the tag check, so distinguish nil from empty), `AdminCacheTTLSeconds int` (yaml `admin_cache_ttl_seconds`, default 300).
  - `ReactionCleanupEnabled *bool` (yaml `reaction_cleanup_enabled`, default true).
  - `EphemeralNoticeEnabled *bool` (yaml `ephemeral_notice_enabled`, default false — off by default since delivery isn't guaranteed and text is chat-specific), `EphemeralNoticeText string` (yaml `ephemeral_notice_text`, default `""`).
  - All applied in `applyDetectionDefaults` (or a sibling `applyFeatureDefaults` called from `Parse`) honoring nil-vs-set for pointers and nil-vs-empty for `FakeAdminSuspiciousTags`. Document each key in `config.example.yaml`.

- [ ] **Step 1: Write the failing test** — parse a minimal YAML that sets none of the new keys ⇒ defaults applied (`FakeAdminEnabled==true`, `FakeAdminMaxDistance==1`, suspicious tags == the 4 defaults, `AdminCacheTTLSeconds==300`, `ReactionCleanupEnabled==true`, `EphemeralNoticeEnabled==false`). Parse a YAML with `fake_admin_enabled: false` and `fake_admin_suspicious_tags: []` ⇒ both explicit values honored (enabled false, tags empty non-nil). Parse `ephemeral_notice_enabled: true` + text ⇒ honored.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the fields, defaults, and example.yaml docs.

- [ ] **Step 4: Run tests, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/config config.example.yaml
git commit -m "Add M5 feature config"
```

---

### Task 12: Wire M5 into main — admin cache, handler cases, feature construction

**Files:**
- Modify: `cmd/tg-antispam/main.go`
- Create: `internal/telegram/admincache.go` (+ `internal/telegram/admincache_test.go`)

**Interfaces:**
- Produces:
  - `internal/telegram`: `type AdminCache struct {...}` with `func NewAdminCache(p Port, ttl time.Duration) *AdminCache` and `func (c *AdminCache) AdminIdentities(chatID int64) []detect.AdminIdentity` — satisfies `detect.AdminSource`. On a miss/expired entry it calls `p.GetChatAdministrators(context.Background(), chatID)` (short TTL per config), maps each `Member` to `detect.AdminIdentity{Username,DisplayName,CustomTitle}` (Note: the `Member` narrow type lacks `CustomTitle` today — extend `Member` with `CustomTitle string` and populate it in `memberFromChatMember` from `ChatMemberAdministrator.CustomTitle`/`ChatMemberOwner.CustomTitle`, library facts §4), caches, and returns; on a port error returns the stale-or-empty slice (fail-open — never blocks detection). Concurrency-safe via a mutex (it's read from the single poll consumer, but keep it safe). **Import note:** `internal/telegram` importing `internal/detect` is allowed (detect stays pure; telegram already sits above detect in the layering — verify no import cycle: detect must NOT import telegram; `AdminSource` is declared in detect and implemented here). If a cycle arises, instead have `AdminCache.AdminIdentities` return `[]detect.AdminIdentity` by declaring a local alias — but the clean path is telegram→detect, which is acyclic.
  - `cmd/tg-antispam/main.go` wiring:
    - Build `adminCache := telegram.NewAdminCache(livePort, time.Duration(cfg.Detection.AdminCacheTTLSeconds)*time.Second)`; set `cascade.Admins = adminCache` and `cascade.FakeAdmin = detect.FakeAdminCfg{Enabled:*cfg...FakeAdminEnabled, SuspiciousTags:cfg...FakeAdminSuspiciousTags, MaxDistance:cfg...FakeAdminMaxDistance}`.
    - Build `memberWatcher := &watch.MemberWatcher{Store: db, Admins: adminCache, AdminChatID: cfg.AdminChatID, Port: livePort, MaxDistance: cfg...FakeAdminMaxDistance, Enabled: *cfg...FakeAdminEnabled}`.
    - Build `reactionCleaner := &watch.ReactionCleaner{Spammers: db, Port: livePort, Enabled: *cfg...ReactionCleanupEnabled}`.
    - Set `machine.EphemeralNotice = *cfg...EphemeralNoticeEnabled; machine.EphemeralText = cfg...EphemeralNoticeText` (or pass via the machine constructor as fits).
    - Add default-handler switch cases (offloaded to `seq.Submit` on the update's chat bucket, mirroring the callback path and honoring the single-consumer rule):
      - `case update.ChatMember != nil:` extract `watch.MemberEvent` from `update.ChatMember` (ChatID=`.Chat.ID`, and the affected user + new name from `.NewChatMember` via the tagged-union `.NewChatMember.User`/status — read the user through whichever union arm is set; use `.From` only for the actor). `seq.Submit(chatID, func(){ if err := memberWatcher.Observe(shutdownCtx, ev); err != nil { log.Printf(...) } })`.
      - `case update.MessageReaction != nil:` extract `watch.ReactionEvent` (ChatID, MessageID, UserID from `.User.ID` when non-nil, `Added: len(NewReaction)>len(OldReaction)`); `seq.Submit(...)` → `reactionCleaner.Observe`.
      - `update.MyChatMember` may be handled minimally (log/ignore) — the admin-cache refresh-on-`chat_member` is satisfied by the cache TTL for M5.

- [ ] **Step 1: Write the failing test** — `admincache_test.go`: a fake `Port` returning two admins (one with a `CustomTitle`); `AdminIdentities(chat)` returns both mapped identities; a second call within TTL does NOT re-call the port (assert call count 1); after TTL expiry it refetches; a port error returns an empty (or last-good) slice without panicking. (main.go wiring is covered by `go build ./...` + the package tests; no separate unit test for the switch.)

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `AdminCache` (+ extend `Member`/`memberFromChatMember` with `CustomTitle`), then the main.go wiring and handler cases.

- [ ] **Step 4: Run full suite + vet + build** — `./scripts/dev.sh test ./...`, `vet ./...`, `build ./...` all green; `-race` on `./internal/watch/... ./internal/store/... ./internal/telegram/...`.

- [ ] **Step 5: Commit**
```bash
git add cmd internal/telegram
git commit -m "Wire M5 features into main"
```

---

## Milestone 5 Definition of Done

- `./scripts/dev.sh test ./...` green (with `-race` on `detect`, `store`, `watch`, `telegram`); `build`/`vet` clean; all new/touched files gofmt-clean.
- Fake-admin detection: a non-trusted, non-admin sender whose name/username/title is within Levenshtein `MaxDistance` of a current admin, OR who carries a suspicious `SenderTag`, is flagged (`fake_admin`) as a cascade stage ahead of behavioral/Bayes; trusted senders skip it; admins are immune (never reach the stage).
- Impersonation-after-join: a `chat_member` rename that newly matches an admin raises a best-effort admin-chat notice; identities are tracked in `user_identity`.
- Reaction cleanup: a reaction ADDED by a user with a prior applied (non-dry-run) spam incident in that chat is removed via `DeleteMessageReaction`; others are left; fail-open on lookup/delete errors; config-gated.
- Ephemeral notice: on a live sanction of a user, if enabled and text is set, a per-user-visible ephemeral message is sent best-effort (never blocks or fails the incident; never the sole verification path).
- All three features are config-gated with documented `config.example.yaml` keys and explicit-false/empty semantics honored.
- Detection stays pure (`internal/detect` imports only domain+stdlib+x/text); only `internal/telegram` and `cmd` touch the bot library; the single inline update consumer is preserved (new updates offloaded via `seq`).

**Deferred to later milestones (tracked):**
- **Blocklists (LOLS/CAS)** — own next milestone; the reaction-cleanup `SpammerSource` and a future blocklist OR-in at the same seam.
- **Photo-change-after-join** — `chat_member` carries no photo; needs `getChatMember`/file hashing, deferred.
- **Admin-cache refresh on `chat_member`** — M5 relies on TTL; event-driven invalidation is a later optimization.
- **Join-request gate (P2)**, **quarantine dialogue** — spec §12 "Later".
