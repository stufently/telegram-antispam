# telegram-antispam — self-hosted Telegram anti-spam bot in Go

> **Антиспам-бот для Telegram-чатов** — лёгкая, самодостаточная альтернатива tg-spam на Go.
> A fast, self-hosted **Telegram anti-spam / moderation bot** written in Go: a multi-stage
> detection cascade, shared CAS/LOLS blocklists, a Bayesian spam filter, and an optional
> LLM check — no CGO, one static binary, SQLite storage.

[![CI](https://github.com/stufently/telegram-antispam/actions/workflows/ci.yml/badge.svg)](https://github.com/stufently/telegram-antispam/actions/workflows/ci.yml)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](https://go.dev)
[![container: GHCR](https://img.shields.io/badge/container-ghcr.io-2496ED?logo=docker)](https://github.com/stufently/telegram-antispam/pkgs/container/telegram-antispam)
[![license: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

telegram-antispam keeps spam, scam links, flooding, and impersonators out of your Telegram
**groups and supergroups**. It runs as a single container, deletes and mutes/bans spammers,
copies the evidence to a private admin chat, and lets moderators record feedback with inline
buttons. It is designed to be **evidence-first** — ordering and an audit trail before
destructive actions — and defaults new chats to dry-run for safe tuning.

**Keywords:** telegram anti-spam bot · telegram spam filter · telegram moderation bot ·
self-hosted · Go / Golang · CAS (Combot Anti-Spam) · LOLS blocklist · Bayesian spam
detection · Kubernetes / Helm · Docker · Prometheus · tg-spam alternative ·
антиспам телеграм · бот модерации.

---

## Features

- **Multi-stage detection cascade** — normalize → admin-immunity gate → global blocklist →
  hard rules → fake-admin impersonation → behavioral (dup/flood/edits) → naive Bayes →
  optional LLM. First hit wins; admins are always immune.
- **Text normalizer / de-obfuscation** — folds homoglyphs, zero-width characters, and
  look-alike Unicode so `Ｃ𝗮𝘀іno` reads as `casino`.
- **Shared blocklists (CAS + LOLS)** — pulls the Combot Anti-Spam (`cas.chat`) and
  `lols.bot` ban lists on a schedule into an atomically swapped in-memory set; each source's
  last-good data survives a partial outage, so refresh failures never block your chat.
- **Bayesian spam filter** — log-space naive Bayes with Laplace smoothing, an idempotent
  `import` command to train from labeled samples, and offline precision/recall calibration.
- **Fake-admin / impersonation detection** — bounded Levenshtein match against the real
  admin list catches users posing as moderators.
- **Behavioral heuristics** — duplicate-message, short-message flood, and edit-after-post
  detection over a rolling window.
- **Optional LLM adjudication (opt-in)** — for messages whose Bayes score sits near the
  threshold (or, with `always_for_untrusted`, for every newcomer message), consult OpenAI
  and/or Anthropic with an `any`/`all` consensus policy.
  **Disabled by default** — no message text ever leaves the process unless you opt in.
- **Captionless-media review** — a newcomer's photo, video or sticker with (almost) no
  caption reaches no text detector at all: rules, Bayes and the LLM all read words. Such a
  message is copied to the admin chat for a human to judge, with **no automatic sanction in
  any chat mode** — a picture is a hint, not proof. Off by default (`media_caption_min_len`).
- **Message shape as an LLM signal** — attachment kinds, "forwarded from a channel" and
  "carries an inline keyboard" are passed to the LLM alongside the text, because the same
  words read differently under a relayed channel post than typed by hand. When the message
  is a reply and the message it answers carries an attachment, the LLM is also told what
  KIND of attachment that was — a bare "обновили наконец" reads differently under an `.apk`
  than under a holiday photo. This covers a reply to a message **in this chat** and a reply
  **across chats** alike: when the parent lives in a channel or a group the bot is not in,
  Telegram sends no `reply_to_message` at all and describes the parent in `external_reply`
  instead, which is precisely the shape a carrier posted to a private channel takes. Either
  way the LLM gets one parent line, worded identically. The external parent is kept on its
  own fields and never folded into the in-chat reply, because that one is the target a
  moderator's `/spam` and `/ham` act on and it must never point at a message outside the
  moderated chat. Only the attachment type crosses; the other person's text never does, and
  the hard rules keep judging attachments on their own message only, so warning someone off
  a malicious file is not itself punishable.
- **Evidence-backed moderation** — evidence is copied to a private admin chat, followed by a
  card **posted as a reply to that copy**, so the pairing survives interleaving: incidents from
  different chats are processed in parallel and an album copies several messages per card, which
  means admin-chat order alone can attach a verdict to the wrong evidence. (If the copy failed
  there is nothing to reply to and the card goes out on its own; if the evidence is deleted
  between the copy and the card, the card is still sent, just unthreaded.) The card says what
  the copy cannot: `copyMessage` strips the origin by design, so the card
  carries the incident id, the reason, whether a sanction is being applied at all (a dry-run
  chat, a review-only verdict and a failed evidence copy all say "nothing applied"), the chat (title and id), the
  message id and the author (`@tag`, numeric id, display name). Attacker-controlled fields are
  sanitized and clipped — a newline or a right-to-left override in a display name would
  otherwise let a spammer forge a line of the card. Telegram silently skips messages it
  cannot copy (a quiz poll, say) and calls that a success, so "copied" is verified by
  counting: nothing copied is handled as a failed copy — no sanction on a probabilistic
  verdict — and a partly copied album is sanctioned with `evidence INCOMPLETE: copied N of M
  messages — the part that triggered the verdict may be missing` on its card, so nobody
  reviews part of a message believing it is all of it.
  The card comes with inline
  **Confirm spam / False positive / Lift (no learn) / Delete evidence** buttons and
  per-callback RBAC. The buttons act: false-positive and lift really unban / unmute the user
  in the source chat, delete-evidence really removes the copies, and confirm/false-positive
  train the Bayes filter. (Deleted messages cannot be restored — Telegram has no such call.)
  A card for an incident that was only reported — a review verdict, or any incident in a
  dry-run chat — carries a fifth button, **Spam: delete + mute**, which applies the sanction
  now: "Confirm spam" deliberately only records and trains, so on such a card it would
  otherwise teach the corpus while leaving the message in the chat.
- **Moderator commands** — reply to any message with `/spam` (or `/spam@yourbot`) to delete
  it, mute its author and train the corpus, `/ban` to remove the author WITHOUT teaching the
  corpus anything (a rule violation is not a spam sample), or `/ham` to lift a sanction and
  relabel that message. Commands run the same incident pipeline as the detector — evidence first, undo
  buttons in the admin chat — and only administrators of that chat (or configured
  operators) may use them; an unresolvable admin list denies rather than allows.
- **Newcomer defenses** — spam-reaction cleanup, ephemeral one-way notices, and a trust
  score that graduates real users out of the strict checks. Warming up an account is not
  free: only messages of a configurable minimum length count, and only DIFFERENT ones —
  repeating "привет" five times earns credit once.
- **Attachment, occurrence and whole-message rules** — configurable blocked document
  extensions/MIME types (the filename itself is discarded), caps on links, mentions and emoji per
  message from an untrusted sender (counting repeats, not distinct values), plus a
  `deny_exact` list that matches only when the entry IS the whole message, for words too
  ordinary to ban as substrings.
- **Chat hygiene** — optional removal of Telegram's own "X joined" / "X left" notices.
- **Dry-run mode** — observe and log verdicts without touching anyone, per chat.
- **Observability** — Prometheus `/metrics`, a `/healthz` endpoint, and a **daily digest**
  of actions to the admin chat, reporting applied, dry-run, and incomplete actions separately.
  Every message a detector rules on leaves one log line — `observed` when it passed,
  `enforced` (with `outcome=succeeded|partial|failed`) or `not enforced` when it raised an
  incident — carrying ids, the action and which detectors fired, and never message text.
- **Startup self-check** — warns if the bot lacks `can_delete_messages` /
  `can_restrict_members`, or if Telegram's native Aggressive Anti-Spam would hide messages.
- **Single static binary** — pure Go (`CGO_ENABLED=0`), pure-Go SQLite, distroless image.

## Why another Telegram anti-spam bot?

telegram-antispam is a **simpler, self-hosted alternative to [tg-spam](https://github.com/umputun/tg-spam)**.
It fixes the identity bugs that plague hash-based spam bots (message identity is
`(chat_id, message_id)`, never a text hash — so captionless media can't collapse into one
key and ban random people), keeps a full audit row for every actionable incident so later
review never loses the reasoning, and never re-imports presets over your learned data. It
ships as one container with no external database.

## Quick start (Docker Compose)

```bash
git clone https://github.com/stufently/telegram-antispam.git
cd telegram-antispam
cp config.example.yaml config.yaml   # edit bot_token and admin_chat_id
mkdir -p data                         # writable SQLite volume
docker compose up -d
```

Minimal `config.yaml`:

```yaml
bot_token: "123456:ABC-your-telegram-bot-token"
admin_chat_id: -1001234567890   # a private group where evidence + buttons are posted
action: delete_mute             # delete_mute | mute | ban | delete_only
chats:
  mode: auto                    # auto | allowlist | owners_only
  start_in_dry_run: true        # observe first, enforce later
```

Add the bot to your group **as an administrator** with *delete messages* and *ban users*
rights. Start in `start_in_dry_run: true`, watch the logs and the daily digest, then take the
chat live by listing it under `chats.enforce`:

```yaml
chats:
  start_in_dry_run: true   # new chats keep observing
  enforce: [-1001234567890] # …this one moderates for real
  force_dry_run: []         # the brake; wins over enforce
```

`start_in_dry_run` only seeds a chat's row the first time the bot sees it, so changing it
later does **not** affect a chat that already registered — `enforce` is what graduates an
observed chat to enforcement, and `force_dry_run` is what pulls it back.

## Run the container directly

```bash
docker run -d --name tg-antispam \
  -e CONFIG_PATH=/config/config.yaml -e DB_PATH=/data/antispam.db \
  -v "$PWD/config.yaml:/config/config.yaml:ro" -v "$PWD/data:/data" \
  -p 9090:9090 \
  ghcr.io/stufently/telegram-antispam:latest
```

## Deploy to Kubernetes (Helm)

```bash
# Production: keep the token in a Kubernetes Secret (never in the ConfigMap).
kubectl create secret generic tg-antispam-token --from-literal=bot_token=123456:ABC...

helm install tg-antispam ./deploy/helm/tg-antispam \
  --set existingSecret=tg-antispam-token \
  --set-file config=./config.yaml   # config.yaml WITHOUT bot_token
```

The bot token is injected as the `BOT_TOKEN` env var from a Secret, so it never lands in the
ConfigMap. Use `--set existingSecret=<name>` to reference a pre-created Secret (recommended),
or `--set botToken=<token>` to let the chart manage one. LLM keys work the same way — set
`llmSecret.name` plus `llmSecret.openaiKey` / `llmSecret.anthropicKey` and they arrive as
`OPENAI_API_KEY` / `ANTHROPIC_API_KEY`, so no credential is written into the ConfigMap.

The chart runs as a non-root user with a read-only root filesystem, no service-account token
and all capabilities dropped, on a PVC-backed `/data` volume with a `Recreate` strategy and
`replicaCount: 1` (SQLite is a single writer). It wires `/healthz` liveness/readiness probes
and ships a ClusterIP Service so Prometheus can scrape `:9090/metrics` at a stable address.
The image tag defaults to `Chart.appVersion`, so chart and image cannot drift.

Two deployment details worth knowing:

- **Changing the config rolls the pod.** The pod template carries a `checksum/config`
  annotation, and it is load-bearing: the bot's file watcher cannot see a ConfigMap update
  (kubelet swaps the mounted `..data` directory instead of writing the file), so without the
  annotation an edited ConfigMap would never reach the running process.
- **The PVC defaults to 10Gi**, not because the database is big — it is tiny — but because
  cloud CSI drivers enforce a minimum (Hetzner's is 10Gi and rejects smaller requests).

## Train the Bayesian filter

```bash
# one labeled sample per line; re-imports are idempotent (no double-counting)
tg-antispam import --label spam --scope global spam-samples.txt
tg-antispam import --label ham  --scope global ham-samples.txt
```

In Kubernetes, put the two files in a ConfigMap and set
`training.existingConfigMap`: the chart then runs the same import as init
containers before every start. That is the supported path because the image is
distroless (no shell to exec into) and the database has a single writer on a
ReadWriteOnce volume, so a separate Job would have to fight the running pod for
the volume.

**Keep the two classes roughly balanced.** The score includes the class priors,
so a mostly-spam corpus shifts *every* message upward — at 80% spam the prior
alone contributes about +1.5, which clears a threshold of 1.0 on its own and
turns ordinary chat into spam verdicts. Calibrate on a holdout with
`detect.Evaluate` rather than trusting the default threshold: on a real 187/447
corpus, raising `bayes_threshold` from 1.0 to 2.0 removed every false positive
at identical recall.

## Answer a card without Telegram

A bot cannot press its own inline keyboard — Telegram only ever delivers a
callback query from a user account — so a reviewer working outside the chat
(an operator on the host, a script, an agent) has no way to answer a card at
all. The `decide` subcommand is that way in:

```bash
tg-antispam decide -preview 12 13     # what these cards are, changes nothing
tg-antispam decide 12 13              # confirm as spam: record + learn
tg-antispam decide -no-train 14       # record the decision, learn nothing
```

It does exactly what the **Confirm spam** button does — takes the same
single-decision claim, writes the same audit sample, trains the same corpus the
message was scored against — and nothing else. That ceiling is deliberate:
confirming touches no one, while every other card action (false positive, lift,
enforce, delete evidence) calls Telegram and either frees a muted user,
sanctions a live one, or destroys evidence. Those stay behind a human pressing a
button.

`-no-train` is for when the sanction was right but the text is a poor example —
a blocklist hit sanctions a known id, and its message may be perfectly
ordinary. It is final: the captured tokens are dropped, so a later plain
`decide` cannot learn what this run refused. Re-running on an already-confirmed
incident is otherwise safe — it finishes an interrupted confirm (claim taken,
training never done) and is a no-op on a complete one. A training failure is
reported and exits non-zero, leaving the tokens for a retry.

The corpus a decision trains is resolved from the config file as it is NOW
(`detection.bayes_scope`), not as it was when the message was scored; changing
that setting is a restart anyway, but a card left unanswered across such a
change would train the new corpus.

Note that confirming does not edit the card, so the buttons stay in the admin
chat; a later press answers "already decided: confirmed spam".

## Enable the optional LLM stage

Opt-in only — it sends borderline message text to a paid, official API. The system prompt is
configurable (`llm.prompt`) and `temperature` / `max_tokens` are sent only when you set them,
because reasoning models reject an explicit temperature and spend a small token budget on
hidden reasoning — returning an empty answer this stage would read as "not spam". In
`config.yaml`:

```yaml
llm:
  enabled: true
  policy: any            # any | all (consensus across providers)
  borderline_band: 0.5   # how close to the Bayes threshold triggers a check
  providers:
    - kind: openai
      api_key: "sk-..."
      model: gpt-4o-mini
    - kind: anthropic
      api_key: "sk-ant-..."
      model: claude-3-5-haiku-latest
```

See [`config.example.yaml`](config.example.yaml) for every option with documented defaults.

## Architecture

A clean, testable layering keeps the detection core pure and the side effects at the edges:

| Package | Responsibility |
|---|---|
| `internal/detect` | **Pure** detection cascade — normalizer, rules, behavioral, Bayes, fake-admin (stdlib + `x/text` only) |
| `internal/telegram` | The only package that talks to the Telegram Bot API; rate-limited outbound queue with 429 retry |
| `internal/incident` | Evidence-before-action state machine with durable incident/audit state |
| `internal/blocklist` | CAS + LOLS syncer with atomic snapshots and per-source last-good data |
| `internal/llm` | Opt-in OpenAI / Anthropic borderline adjudication with consensus |
| `internal/store` | SQLite (WAL, single writer), migrations, audit log |
| `internal/ops` | Prometheus metrics, `/healthz`, daily admin digest |
| `internal/config` | YAML config load, validation, and hot-reload |

See [`docs/architecture.md`](docs/architecture.md) for the current runtime flow,
safety invariants, config-reload scope, and implementation boundaries.

## Configuration reference

Every field is optional except `bot_token` and `admin_chat_id`; unset fields fall back to
documented defaults. The blocks are `chats`, `detection` (rules, behavior, Bayes,
fake-admin), `blocklist` (CAS/LOLS), `ops` (metrics/digest), and `llm`. Full annotated
example: [`config.example.yaml`](config.example.yaml).

## Build from source

```bash
go build -o tg-antispam ./cmd/tg-antispam    # Go 1.25+, CGO_ENABLED=0
```

Or run the test suite in Docker (no local toolchain needed):

```bash
./scripts/dev.sh test ./...
```

## FAQ

**Does it need a database server?** No. It uses embedded SQLite (pure Go, no CGO) on a
writable volume.

**Will it ban people by accident?** Every action is evidence-backed, administrator lookups
fail safely, and you can run any chat in dry-run first. If one slips through, `False positive`
and `Lift` unban or fully unmute the user from the admin chat — though the deleted messages
themselves cannot be brought back, and each incident accepts one decision only (an old button
must not lift a newer, unrelated sanction).

**Does it send my users' messages to a third party?** Only if you explicitly enable the LLM
stage. By default nothing leaves the process.

**Which Telegram API?** The Bot API via [go-telegram/bot](https://github.com/go-telegram/bot)
(long polling). No MTProto / user account required.

## License

[MIT](LICENSE) © stufently.
