# telegram-antispam — Design Specification

**Status:** approved 2026-08-18 · **License:** MIT · **Language:** Go · **Repo language:** English

A single-process antispam bot for Telegram that protects many chats at once. It is a
deliberately simpler and more correct alternative to `umputun/tg-spam`: one binary
serves 50–300 chats (~200k messages/day), forwards the offending media to a shared
admin chat as evidence, learns from four admin buttons, and defaults to reversible
actions.

This document is the single source of truth for the design. Implementation follows the
writing-plans skill after the spec is reviewed.

---

## 1. Goals and non-goals

### 1.1 Goals

1. **One process, many chats.** No per-chat instance, no per-chat restart. Registering
   or de-registering a chat never requires a redeploy.
2. **Evidence before action.** The content a message is banned for (photos, albums,
   documents) is copied to the admin chat *before* any destructive action, because
   `banChatMember` in a supergroup always deletes the user's prior messages.
3. **Reversible by default.** The default action is delete + mute, not ban. Every
   automatic decision is one tap to undo, globally.
4. **Simple training.** Learn from admin buttons and from `spam.txt` / `ham.txt`, plus
   optional offline LLM labeling of chat history. No reverse-parsing of the bot's own
   rendered messages (umputun's fragile `getCleanMessage`).
5. **Cheap-to-expensive detection.** A short-circuit cascade; the LLM runs only on
   genuinely borderline messages so cost stays predictable at scale.
6. **Correct Telegram integration.** First-class albums, forums/topics, discussion
   groups, edited messages, reactions, and the four sender identities.

### 1.2 Non-goals (explicit, so they don't creep in)

- **No web UI.** On principle. All operation is via the admin chat + YAML + CLI.
- **No graphical captcha.** Button/image captchas are solved by human farms and
  MTProto userbots; they mostly repel real users. Newcomer soft-restrict replaces them.
- **No federation marketplace** with third-party bots.
- **No local OCR / voice transcription in v1.** Expensive, high false-positive, a
  separate product. Revisit only on demand.
- **No full moderation suite** (warns/notes/karma/games/welcome HTML). This is an
  antispam tool, not a Rose clone.
- **MTProto is deferred** and, if adopted, is an optional module only — see §12.

---

## 2. Scale and platform assumptions

| Dimension | Assumption |
|---|---|
| Chats served | 50–300 supergroups by one process |
| Throughput | ~200k messages/day, bursty (raids) |
| Transport | Bot API long polling by default; optional webhook |
| Library | `github.com/go-telegram/bot` (v1.23.0, supports Bot API 10.2) |
| Storage | SQLite via `modernc.org/sqlite` (cgo-free), WAL, single writer |
| Rate limits | ~30 req/s global, 20 msg/min per group, 1 msg/s per chat |

**Update-loss window:** Telegram retains updates for at most 24h. Downtime beyond that
loses events irrecoverably — the design accepts this and never assumes a complete
history.

**`allowed_updates` (load-bearing).** The bot subscribes to: `message`,
`edited_message`, `callback_query`, `chat_member`, `my_chat_member`,
`message_reaction`. `chat_join_request` is added when the join-request gate ships
(§11, P2). `chat_member` — not `new_chat_members` — is mandatory: the latter is not
reliably delivered for large chats/premium users; newcomer detection keys on the
`ChatMemberLeft/None → ChatMemberMember` transition.

---

## 3. Architecture overview

Approach C (hybrid), chosen over a flat script and over a fully layered framework:

- **Detection is flat, pure functions.** Normalize → signals → verdict. No side
  effects, no Telegram types, trivially table-testable. This is where umputun's
  complexity was *not* (detection is ~17% of its lines); keeping it flat keeps it
  simple.
- **Side effects live in an incident state machine.** Everything with ordering,
  retries, rate limits, or irreversibility (evidence, actions, admin dialogue, audit)
  is modeled as an explicit incident with persisted states. This is where umputun's
  bugs actually lived, so this is where structure is spent.

```
Telegram update
      │
      ▼
 telegram adapter ──► domain.Message (library types stop here)
      │
      ▼
 detect cascade (pure) ──► domain.Verdict{action, scope, signals, confidence}
      │
      ▼
 incident state machine ──► action executor ──► outbound queue ──► Telegram port
      │                                                              │
      ├─► admin chat (evidence, buttons, RBAC)                       │
      └─► store (incidents, audit, samples, users, chats) ◄──────────┘
```

### 3.1 Package layout

```
cmd/tg-antispam/          entry point, wiring, signal handling
internal/domain/          Message, Sender, Verdict, Incident, Signal — pure types
internal/telegram/        the ONLY package importing the bot library; adapter + port
internal/detect/          normalize, trust, rules, blocklist, behavior, bayes, llm/
internal/incident/        state machine: evidence → action → audit
internal/action/          executor over the Telegram port (mute/ban/delete/reactions)
internal/queue/           outbound: global + per-chat rate limit, priorities, retry
internal/admin/           admin chat: evidence rendering, callbacks, RBAC, commands
internal/store/           SQLite: migrations, repos, single writer goroutine
internal/train/           sample import, offline LLM labeling of history
internal/config/          YAML load, validation, atomic hot-reload
internal/metrics/         prometheus + /healthz + daily digest
```

**Dependency rule:** `domain` imports nothing project-specific. `detect` imports only
`domain`. Only `internal/telegram` imports the bot library. This makes the whole
detection path and the incident logic testable without a network.

### 3.2 The Telegram port

`internal/telegram` exposes a narrow interface the rest of the code depends on, so
incident/action logic is tested against a fake:

```go
type Telegram interface {
    CopyMessages(ctx, dstChat, srcChat int64, ids []int) ([]int, error)
    DeleteMessages(ctx, chat int64, ids []int) error
    BanMember(ctx, chat, user int64) error
    RestrictMember(ctx, chat, user int64, perms Perms, until int64) error
    BanSenderChat(ctx, chat, senderChat int64) error
    DeleteUserReactions(ctx, chat, user int64) error          // Bot API 10.0
    GetChatMember(ctx, chat, user int64) (Member, error)
    GetChatAdministrators(ctx, chat int64) ([]Member, error)
    SendAdmin(ctx, msg AdminMessage) (int, error)
    SendEphemeral(ctx, chat, receiver int64, msg Message) error // Bot API 10.2
    AnswerCallback(ctx, id string, text string) error
}
```

---

## 4. Sender identity and immunity

Every update is classified before detection:

| Identity | Detection | Handling |
|---|---|---|
| Normal user | `from` is a user, `sender_chat == nil` | full pipeline |
| External channel | `sender_chat.type == channel`, id ≠ chat, ≠ linked | `banChatSenderChat` on hard hit |
| Anonymous admin | `sender_chat.id == chat.id` (GroupAnonymousBot) | skip |
| Linked-channel autoforward | `is_automatic_forward`, `from.id == 777000`, id == `linked_chat_id` | skip (never trigger on the channel's own post) |

Admins and the bot itself are always immune. The admin chat is excluded from the
moderation pipeline entirely. Admin lists are cached in memory with a short TTL and
refreshed on `chat_member`/`my_chat_member` for that chat.

---

## 5. Detection cascade

Pure functions, short-circuit, cheap → expensive. Verdict is
**`hard_hit OR bayes_score >= threshold`** — not a weighted ensemble.

```
0. normalize            (always; see §5.1)
1. immunity/identity     (§4) — skip immune senders
2. trust-gate            (§5.2) — decide which later stages a trusted user skips
3. hard rules            (everyone) — deny/allow lists, entity/link rules
4. blocklists            (everyone) — LOLS primary, CAS supplementary (§5.3)
5. behavioral            (everyone) — duplicates, short-message flood, edits, raids
6. bayes                 (non-trusted) — naive Bayes over normalized tokens
7. llm borderline        (non-trusted, only if score near threshold) — consensus (§5.4)
```

Any hard hit at 3/4/5 short-circuits to a verdict. Bayes and LLM only decide the
borderline. A trusted user (§5.2) skips **only** the expensive semantic stages (Bayes
tie-breaks, LLM); stages 3–5 run for everyone.

### 5.1 Normalizer (central component, not a utility)

Every detector runs over one flat normalized representation, never the raw message.
The normalizer collapses into text + extracted links + feature flags:

- `text`, `caption`
- **`rich_message.blocks`** (Bot API 10.1/10.2) — otherwise every rich message is
  invisible to text detectors
- all `entities`, including hidden `text_link` and `custom_emoji`
- **`external_reply`** content
- **`sender_tag`** (a spammer can self-label "Admin")
- user-added poll options (`PollOption.added_by_user`)
- de-obfuscation: homoglyph/confusables folding (Cyrillic/Latin/Greek mixing),
  zero-width stripping, intra-word spacing collapse ("п и ш и в л с")

If a new Telegram surface is added and the normalizer isn't taught about it, it becomes
a blind spot — so new payload surfaces are normalizer changes first.

### 5.2 Trust-gate

A user becomes trusted after **N=5 meaningful messages** (configurable; "meaningful"
excludes trivial "+"/emoji-only/stickers). Trust is observation-based — Telegram does
not expose account age via Bot API, so "days" is measured from first observation, not
signup.

Trust **only** disables the expensive semantic stages. It never grants immunity: hard
rules, blocklists, duplicate/flood/edit checks, and `/report` apply to everyone. This
is the explicit defense against warmed-up and hijacked accounts. `auto_established`
(earned) and `explicitly_approved` (admin button) are stored separately.

### 5.3 Blocklists

- **LOLS primary** — ~3.9M ids, hourly delta sync.
- **CAS supplementary** — ~1.2M ids, periodic full export.

Measured intersection is only ~255k, so union (~4.86M) roughly quadruples coverage vs
CAS-only (what tg-spam uses). Lookups are in a bounded in-memory cache with a short
timeout and **fail-open** (a blocklist outage never blocks the chat).

### 5.4 LLM (borderline only)

Two providers configured as a list with a consensus policy (`any` | `all`). With one
provider configured it is a plain single call — no extra cost. Runs only when the
Bayes score sits near the threshold. Paid official APIs. Message text sent to an
external LLM is gated by config (`llm.enabled`, provider list) and covered by the
privacy/retention rules in §9.

### 5.5 Fake-admin detection (v1)

- Newcomer/username within **Levenshtein distance 1** of a current admin's name or
  username → flag.
- **Name/photo change after join** tracked via `chat_member` updates.
- Suspicious **`sender_tag`** (e.g. "Admin", "Support", "Verified") on a non-admin.

This is content-independent and catches the impersonation attack that content-only
bots (Rose, others) miss.

---

## 6. Verdict, action, and scope

A `Verdict` carries `action`, `scope`, `signals[]`, `confidence`, and a human-readable
reason.

### 6.1 Actions

Config selects the automatic action: `delete_mute` (default) | `ban` | `mute` |
`delete_only`. Independently, reaction cleanup and ephemeral newcomer replies are
toggles.

### 6.2 Scope — global mute, admin-only permanent ban

Locked policy: automatic actions apply as a **global mute** across all served chats;
**permanent ban** is reserved for an admin button or a hard rule (CAS/LOLS hit, known
scam). This keeps a single false positive cheap (a mute, globally undone in one tap)
while a spammer is silenced everywhere at once. One-tap **global undo** is a
first-class admin control.

### 6.3 Quarantine — the third verdict

Borderline cases resolve to **quarantine**, a real incident state, not a binary
delete/allow:

1. The message is removed from the chat but the full copy is preserved as evidence.
2. The admin chat shows it with a **Restore** button.
3. On Restore, the bot **reposts the content on the author's behalf with attribution**,
   and records ham feedback.

This directly answers the most-requested (2018→2026) feature: "spam / not spam /
not sure".

### 6.4 Hard safeguards (each prevents a concrete failure)

- **Never auto-delete `is_paid_post` for 24h** — deletion inside the window costs the
  chat owner real money.
- **Mute `until_date` strictly within [30s, 366d]** — outside this range Telegram
  silently makes the restriction permanent ("muted for a day, banned forever").
- **New rule starts in record-only** and auto-pauses on a per-chat hit-count ceiling —
  one careless stop-word can't wipe a live chat while the admin sleeps.

---

## 7. Incident state machine (ordering is an API requirement)

Every actionable verdict becomes an incident keyed by `(chat_id, message_id)`. The
order below is dictated by Telegram semantics, not style:

```
1. INSERT incident (pending), key (chat_id, message_id)
2. album buffer: wait ~700ms on (chat_id, media_group_id), tombstone late items
3. copyMessages → admin chat/topic     ← evidence BEFORE any destructive action
4. save admin_message_id(s); state = evidenced
5. apply action: mute / ban / delete-only per config; scope per §6.2
6. delete originals (deleteMessages, batches ≤100) if still within 48h
7. record each step's result separately (partial failure is explicit)
```

- **Albums:** umputun has no `media_group_id` handling — the direct cause of the
  user's own false-positive ban. We buffer album parts on `(chat_id, media_group_id)`
  and copy the whole group with `copyMessages` (1–100 ascending ids). A late part of
  an already-decided album is attached and deleted immediately.
- **`copyMessage` loses the source link**, so a separate summary with source
  chat/message/user, signals, and buttons is sent. `callback_data` is limited to 64
  bytes, so it carries only an opaque incident id; everything else is looked up in the
  store.
- **Evidence TTL ≤ 47h** (Bot API deletes ordinary messages only within 48h). The TTL
  job survives restart. The reliable "evidence auto-expiry" mechanism is Telegram's
  native auto-delete timer on the admin chat, not a bot-side delete past 48h.
- **Evidence-copy failure policy is explicit:** low-confidence → don't act, send a text
  alert; hard deny → store metadata/`file_id`, act, record `evidence_failed`.
- Exactly-once against Telegram is impossible; rare duplicate evidence is acceptable,
  duplicate **bans/training is not** — dedup on `(chat_id, message_id)` and on
  `update_id`.

---

## 8. Admin chat

One shared admin chat for all served chats (may itself be a group or forum; per-chat
destination topic is configurable).

**Four buttons**, each a distinct outcome (per-callback RBAC — the presser must be an
admin of the *source* chat or a global operator; checking the admin chat id alone is
insufficient):

1. **False positive** — unrestrict/unban + ham + explicit approve.
2. **Lift without learning** — reverse the action, no training.
3. **Confirm spam** — spam + permanent ban.
4. **Delete evidence** — remove the copied evidence.

**Reports are aggregated.** The 20 msg/min-per-group limit bites notifications, not
sanctions: banning 500 raiders is fine; announcing it 500 times is not. Batch raids
into one admin message. A **daily digest** summarizes activity.

`/report` (reply to a message): a reporter who has passed the trust-gate contributes to
a **unique-reporter threshold** that yields a **temporary mute** + an admin incident —
never an automatic ban. This is the coordinated-report-resistant form of the feature
umputun refused.

---

## 9. Storage, privacy, retention

SQLite, WAL, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL`; pragmas per
connection via DSN; a single writer goroutine; short transactions; backups via SQLite
backup API / `VACUUM INTO` (never a bare `.db` copy).

Minimal keys:

- `users PRIMARY KEY(chat_id, user_id)` — trust counters, approval flags, last seen
- `incidents UNIQUE(chat_id, message_id)` — state, admin_message_ids
- `updates PRIMARY KEY(update_id)` — idempotency
- `samples(scope, label, normalized_hash)` unique, with `origin ∈ (preset, user)`
- `evidence(incident_id, admin_chat_id, admin_message_id)`
- `chat_aliases(old_id, new_id)` — basic-group → supergroup migration
- `audit` — the full verdict with all signals, survives unban
- `jobs/outbox` — TTL cleanup and ret/retry

**Identity rules (each fixes a real umputun bug):**

- Message identity is `(chat_id, message_id)`, **never a text hash** — umputun's
  `sha256(text)` PK collapsed all captionless media into `sha256("")` and banned
  random people.
- `samples.origin` keeps preset re-import from wiping learned data.
- The audit row keeps the full verdict so an unban never loses the reasoning.

**Privacy/retention (self-host expectations):**

- Ham bodies are not stored — only `user_id, chat_id, trust_count, last_seen`.
- Configurable retention for messages/samples/evidence with automatic purge.
- `delete_user_data <id>` command and JSON export.
- LLM providers are explicit in config; sending content externally is opt-in.
- Minimal PII in Prometheus labels.

---

## 10. Chat lifecycle and config

**YAML is the single source of truth** with **atomic, validated hot-reload**: on file
change, parse + validate a candidate config; only swap in on success; a bad file logs
and keeps the running config. No restart to add a chat.

Chat-registration policy is a config key:

```yaml
chats:
  mode: auto | allowlist | owners_only
  start_in_dry_run: true          # a new chat only reports to admin, acts on nothing
```

`my_chat_member` drives lifecycle: on add, verify `can_delete_messages` and
`can_restrict_members`; on `left/kicked`, mark the chat disabled and stop its jobs but
retain data. Basic-group → supergroup migration moves config/users/samples/jobs in one
transaction and stores an old→new alias; ids are `int64`.

**Discussion groups are first-class:** auto-detect `linked_chat_id`, run the same
global bans, and allow separate thresholds (comments are shorter and more ad-like).
**Forums/topics:** per-topic policy scope; store the source `message_thread_id`;
moderation is chat-wide (restrict/ban can't be limited to one topic) but the admin
destination topic is configurable.

---

## 11. Outbound queue and rate limiting

A global token bucket + per-destination-chat bucket. The queue is **priority-ordered**:
delete/ban above notifications and TTL cleanup. It honors `429` `Retry-After`.
`deleteMessages` goes in batches of 100. A stuck destination never blocks others.

---

## 12. New-platform features and phasing

Verified against the official Bot API changelog and against the library source
(v1.23.0).

**In v1 (library supports all three):**

- **Reaction cleanup** — `deleteMessageReaction` / `deleteAllMessageReactions` with
  `user_id` (Bot API 10.0). Closes the reaction-spam request open since 2022 that the
  market still calls unsolvable.
- **Ephemeral newcomer replies** — Bot API 10.2; the bot↔newcomer dialogue is visible
  only to that user, so the bot stops littering the chat. Caveat: delivery is not
  guaranteed, so an ephemeral message is never the sole verification path.
- **Fake-admin detection** — §5.5.

**Later:**

- **Join-request gate (P2)** — screen applicants pre-entry, including `bio` (present on
  `ChatJoinRequest`, absent from `getChatMember`). Blocked on: the library lacks Join
  Request Queries (no `query_id`, no `answerChatJoinRequestQuery`), so it needs a raw
  HTTP call, and the handler has a hard **10-second** budget — fast hot-cache path only,
  queue as the safe default on timeout.
- **Communities (Bot API 10.2)** — potential native chat grouping; evaluate later.

**MTProto experiment — resolved 2026-08-18 (measured, not assumed):** a throwaway
probe authenticated a bot session over MTProto (`gotd/td`, official `api_id`) and
measured two things against a real supergroup where the bot is admin:

- `channels.getParticipants` — **works under a bot token** (returned the full member
  list with access hashes). This capability is impossible via Bot API and would enable
  retro-cleanup of already-seated bots.
- `users.getFullUser` on real members — **succeeds, but every account-age field is
  absent**: `PeerSettings.registration_month`, `phone_country`, `name_change_date`,
  and `photo_change_date` all came back unset for two real members and for the bot
  itself. **Telegram does not populate these fields for a bot session.**

Conclusion: the account-age signal — MTProto's strongest justification — is not
available to bots, so it is dropped. Member enumeration alone does not justify a second
network stack, a mandatory per-install `api_id` registration, and userbot-ban exposure
against the project's primary "much simpler" goal. **MTProto is not part of the project**
unless a future, specific need reopens it as a strictly optional module. The v1
fake-admin detector (§5.5) is unaffected: it tracks name changes via `chat_member`
updates and Levenshtein-matches admin names, needing none of these MTProto fields.

---

## 13. Deployment, ops, CI

- **Deploy:** docker-compose and a Helm chart; public image on GHCR. Container runs as
  a non-root user; SQLite on a writable volume.
- **Observability:** Prometheus `/metrics`, `/healthz`, daily digest to the admin chat.
  No web UI.
- **CI:** `go test -race`, `golangci-lint`, GHCR image build, `goreleaser`.
- **Self-checks:** on startup and on `my_chat_member`, verify required admin rights per
  chat and warn on missing `can_delete_messages`/`can_restrict_members`; detect and warn
  when native Aggressive Anti-Spam is enabled (it deletes messages before us and we
  can't see why).

---

## 14. Testing strategy

- **Pure unit tests** — normalization, trust, rules, Bayes, verdict policy (table-driven).
- **Workflow tests** — the incident state machine against a fake Telegram, exercising a
  failure at each step (evidence copy, action, delete).
- **Album timing** — buffer/tombstone with a fake clock.
- **Idempotency** — duplicate `update_id`, restart mid-incident, no double ban/train.
- **Migration** — basic-group → supergroup alias.
- **Callback RBAC and replay.**
- **Adapter contract tests** — `httptest.Server` with JSON fixtures for the library.
- **Store tests** — against a real temporary SQLite DB.
- **Regression corpus** — precision/recall on a held-out set, RU soft-job/crypto spam
  included, not just individual messages.

---

## 15. Build order (for the implementation plan)

1. Domain types, Telegram port, config load/validate, Telegram JSON fixtures.
2. Store: migrations, chat lifecycle, dedup, the incident state machine.
3. Long polling + per-chat sequencer.
4. Outbound queue, rate limiting, retry, rights checks.
5. Evidence/album/admin callbacks with RBAC.
6. Trust + a few strict heuristics + normalizer, running in dry-run.
7. Idempotent import + global-seed & local Bayes, offline calibration.
8. Blocklists (LOLS + CAS) with timeout, bounded cache, fail-open.
9. Reaction cleanup, ephemeral replies, fake-admin detection.
10. Docker/Helm, health/metrics/digest, backup/restore, load/failure tests.
11. LLM borderline (consensus) — last, once the corpus shows it is needed.

MVP correctness order is **delivery → ordering → reversibility first**, detectors
second. The seven detectors are worthless if evidence or ordering is wrong.
