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
copies the evidence to a private admin chat, and lets your moderators reverse any action with
one tap. It is designed to be **correct and reversible first** — evidence and ordering before
detectors — so a false positive is always undoable.

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
  `lols.bot` ban lists on a schedule into a bounded in-memory set, **fail-open** so a
  blocklist outage never blocks your chat.
- **Bayesian spam filter** — log-space naive Bayes with Laplace smoothing, an idempotent
  `import` command to train from labeled samples, and offline precision/recall calibration.
- **Fake-admin / impersonation detection** — bounded Levenshtein match against the real
  admin list catches users posing as moderators.
- **Behavioral heuristics** — duplicate-message, short-message flood, and edit-after-post
  detection over a rolling window.
- **Optional LLM adjudication (opt-in)** — for messages whose Bayes score sits near the
  threshold, consult OpenAI and/or Anthropic with an `any`/`all` consensus policy.
  **Disabled by default** — no message text ever leaves the process unless you opt in.
- **Reversible moderation** — evidence is copied to a private admin chat with inline
  **Confirm spam / False positive / Lift (no learn) / Delete evidence** buttons and
  per-callback RBAC.
- **Newcomer defenses** — spam-reaction cleanup, ephemeral one-way notices, and a trust
  score that graduates real users out of the strict checks.
- **Dry-run mode** — observe and log verdicts without touching anyone, per chat.
- **Observability** — Prometheus `/metrics`, a `/healthz` endpoint, and a **daily digest**
  of actions to the admin chat.
- **Startup self-check** — warns if the bot lacks `can_delete_messages` /
  `can_restrict_members`, or if Telegram's native Aggressive Anti-Spam would hide messages.
- **Single static binary** — pure Go (`CGO_ENABLED=0`), pure-Go SQLite, distroless image.

## Why another Telegram anti-spam bot?

telegram-antispam is a **simpler, self-hosted alternative to [tg-spam](https://github.com/umputun/tg-spam)**.
It fixes the identity bugs that plague hash-based spam bots (message identity is
`(chat_id, message_id)`, never a text hash — so captionless media can't collapse into one
key and ban random people), keeps a full audit row for every verdict so an unban never loses
the reasoning, and never re-imports presets over your learned data. It ships as one container
with no external database.

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
rights. Start in `start_in_dry_run: true`, watch the logs and the daily digest, then flip
to enforcing.

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
helm install tg-antispam ./deploy/helm/tg-antispam \
  --set image.tag=latest \
  --set-file config=./config.yaml
```

The chart runs as a non-root user on a PVC-backed `/data` volume with a `Recreate` strategy
and `replicaCount: 1` (SQLite is a single writer), and wires `/healthz` liveness/readiness
probes. Prefer a Kubernetes `Secret` for `bot_token` in production.

## Train the Bayesian filter

```bash
# one labeled sample per line; re-imports are idempotent (no double-counting)
tg-antispam import --label spam --scope global spam-samples.txt
tg-antispam import --label ham  --scope global ham-samples.txt
```

## Enable the optional LLM stage

Opt-in only — it sends borderline message text to a paid, official API. In `config.yaml`:

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
| `internal/incident` | Evidence-before-action state machine; every verdict is auditable and reversible |
| `internal/blocklist` | CAS + LOLS syncer with a bounded, fail-open in-memory set |
| `internal/llm` | Opt-in OpenAI / Anthropic borderline adjudication with consensus |
| `internal/store` | SQLite (WAL, single writer), migrations, audit log |
| `internal/ops` | Prometheus metrics, `/healthz`, daily admin digest |
| `internal/config` | YAML config load, validation, and hot-reload |

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

**Will it ban people by accident?** Every action is evidence-backed and reversible from the
admin chat, and you can run any chat in dry-run first.

**Does it send my users' messages to a third party?** Only if you explicitly enable the LLM
stage. By default nothing leaves the process.

**Which Telegram API?** The Bot API via [go-telegram/bot](https://github.com/go-telegram/bot)
(long polling). No MTProto / user account required.

## License

[MIT](LICENSE) © stufently.
