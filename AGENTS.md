# Repository guide

This file applies to the whole repository. It is a compact guide for coding
agents and contributors; user-facing setup remains in `README.md` and
`config.example.yaml`.

## Sources of truth

- Running code and its tests define current behavior.
- `config.example.yaml` documents the supported operator configuration and
  defaults. Keep it synchronized with `internal/config`.
- `docs/architecture.md` describes the current runtime wiring and deliberate
  implementation boundaries.
- `docs/superpowers/specs` and `docs/superpowers/plans` are design and delivery
  history. All M1-M7 checklists are complete, but their intermediate-state
  prose can be stale; do not use it over current code and tests.

## Project shape

`tg-antispam` is one Go process using long polling and embedded SQLite. The
entry point and dependency wiring live in `cmd/tg-antispam`; business packages
are under `internal`:

| Path | Responsibility |
|---|---|
| `internal/domain` | Dependency-free shared types and enums |
| `internal/telegram` | Telegram adapter, narrow `Port`, live/fake ports, albums, per-chat sequencing |
| `internal/detect` | Normalization and the detection cascade |
| `internal/incident` | Evidence-first moderation state machine |
| `internal/admin` | RBAC-gated admin callback handling |
| `internal/store` | SQLite schema, migrations, serialized writes, repositories |
| `internal/queue` | Outbound priority queue, rate limits, Telegram 429 retry |
| `internal/blocklist` | Fail-open CAS/LOLS snapshots and refresh loop |
| `internal/llm` | Optional fail-open borderline adjudication |
| `internal/watch` | Member identity and reaction watchers |
| `internal/ops`, `internal/selfcheck` | Health, metrics, digest, and rights checks |
| `internal/train` | Idempotent offline Bayes sample import |

See `docs/architecture.md` before changing the message path, concurrency,
moderation ordering, config reload, or shutdown.

## Development commands

The supported development path runs Go in Docker and keeps caches in the
repository:

```bash
make test
make vet
make build
make tidy
```

Equivalent commands, including targeted tests, are:

```bash
./scripts/dev.sh test ./...
./scripts/dev.sh test ./internal/detect -run TestName
./scripts/dev.sh test -race ./...
./scripts/dev.sh vet ./...
./scripts/dev.sh build ./...
```

If a matching host Go toolchain is used in a restricted environment, set
`GOPATH=$PWD/.gopath` and `GOCACHE=$PWD/.gopath/cache`. Go may still attempt a
toolchain or module download, so the Docker script is the reproducible path.

Before handing off a Go change, run targeted tests while iterating, then at
least `make test` and `make vet`. Format changed Go files with `gofmt`. CI also
runs `go test -race ./...` and golangci-lint; dependency changes require
`go.mod` and `go.sum` to stay tidy.

## Non-negotiable invariants

- Message identity is `(chat_id, message_id)`, never a text hash. Incident
  dedup uses the first message ID; media-group message IDs must be sorted
  before `copyMessages`.
- The incident row and audit verdict are inserted atomically. The audit row
  records a verdict, not an outcome — it predates the dry-run gate and the
  action itself — so readers must join the incident's `dry_run` and `state`
  rather than treat a row as proof the action happened. Evidence is
  copied to the admin chat before a destructive moderation action,
  and originals are deleted last. Dry-run still records and copies evidence
  but performs no sanction or deletion. Preserve this order in
  `internal/incident`.
- Current chat admins are immune before every detector. When the admin lookup
  fails, a stale list may come back with the error: honour a match on it
  (immunity) but never read absence from it as proof, and defer everyone else
  without granting trust — a deferred message is still recorded in every
  behavioral window, since the update is never reprocessed. `my_chat_member` events and the
  `chat_member` events that touch the admin roster invalidate the TTL cache,
  and an invalidation beats a fetch already in flight (that fetch's result is
  neither cached nor returned). Anonymous admins and linked-channel posts
  are filtered earlier by the Telegram handler. Do not move a detector ahead
  of either immunity gate.
- Downstream detectors consume the central normalized representation. Add new
  Telegram text surfaces in the adapter/normalizer instead of re-parsing raw
  library types inside detectors.
- Trust does not bypass the global blocklist or hard rules. It only skips the
  newcomer-oriented semantic stages such as fake-admin and Bayes checks.
- `internal/telegram` is the only package allowed to depend on the Telegram
  client library. Other packages depend on the consumer-owned `telegram.Port`;
  extend the live and fake implementations together.
- Every outbound `telegram.Port` operation goes through `queue.Dispatcher`
  (the library owns long polling itself). Destructive calls have high priority,
  and Telegram 429 errors become `queue.RetryAfter`. Caller cancellation must
  reach an in-flight queued request.
- Inbound updates use one inline polling worker. Slow DB/network work is
  submitted to `Sequencer`; accepted work is FIFO per chat and different chats
  may run concurrently. `Submit` deliberately drops on a full per-chat queue
  instead of blocking all polling. Do not raise the polling worker count or
  bypass the sequencer without a replacement ordering design.
- SQLite writes go through `DB.Write`, which owns the single writer goroutine;
  reads use `DB.Read`. Migrations must be idempotent for both fresh and older
  databases. Do not store raw message text unless the privacy design is
  intentionally changed and documented.
- Preserve unset-versus-explicit-zero semantics in configuration. Boolean and
  numeric fields where `false` or `0` is meaningful use pointers; defaults,
  validation, YAML example, and config tests must change together.
- New chats default to dry-run. External failures must not manufacture a spam
  result: LLM errors return not-spam and each blocklist source retains its own
  last-good data. Conversely, an unreadable admin or stored chat lifecycle gate
  must stop moderation rather than guessing that live enforcement is safe.
- Shutdown uses separate signal and work contexts. Stop background producers
  (bounded to 10s), flush albums, drain the per-chat sequencer while the
  dispatcher remains live, then cancel the dispatcher and close SQLite. The
  drain has its own 30s bound, armed only after the producers have stopped.

## Change patterns

- Put focused tests beside the changed package. Use `telegram/fake` for port
  behavior and `httptest` for external HTTP integrations; unit tests must not
  require Telegram, CAS, LOLS, OpenAI, or Anthropic credentials.
- A new config key normally needs the struct field, defaulting, validation,
  explicit-value tests, `config.example.yaml`, and startup wiring.
- A new Telegram operation needs a `Port` method, `LivePort` implementation,
  fake implementation/logging, compile-time interface checks, tests, and an
  intentional queue priority.
- A schema change needs an idempotent migration and tests for both a fresh DB
  and a representative pre-change schema.
- Keep detection logic deterministic and explainable: return a stable signal
  name and add cascade-order tests when introducing or moving a stage.
- Do not commit `config.yaml`, `.env`, database files, bot tokens, or LLM API
  keys. `BOT_TOKEN` overrides the YAML token and is the preferred secret path.
