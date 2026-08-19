# telegram-antispam M7 — Deployment, Observability & CI — Implementation Plan

> **Status: ✅ Implemented, reviewed, and merged to `main`.** Every step below is complete; the whole-branch review passed. Checkboxes are ticked for historical record.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Make the bot deployable and observable (spec §13): a dependency-free Prometheus `/metrics` + `/healthz` HTTP server, a daily digest to the admin chat, a hardened Dockerfile, docker-compose + a Helm chart, and a GitHub Actions CI pipeline (test -race, golangci-lint, GHCR image, goreleaser).

**Architecture:** A new leaf package `internal/ops` owns observability: a tiny thread-safe metrics registry that exposes counters/gauges in Prometheus text-exposition format (NO external dependency — keeps the "much simpler" goal), an HTTP server serving `/healthz` and `/metrics`, and a daily-digest builder that queries the `audit` table for a 24h summary and sends it via the existing `telegram.Port.SendAdmin`. `main` starts the ops server and the digest scheduler under the shutdown context, and increments metrics at the existing wiring seams. Deployment artifacts (Dockerfile, compose, Helm, CI) are declarative and verified by build/lint/validate rather than unit tests.

**Tech Stack:** Go 1.24+, stdlib only for the ops code (`net/http`, `sync`, `sort`, `fmt`, `strconv`). Docker (`golang:1.26` builder → minimal non-root runtime). GitHub Actions, golangci-lint, goreleaser. `modernc.org/sqlite` unaffected.

**Spec:** `docs/superpowers/specs/2026-08-18-telegram-antispam-design.md` §13 (deploy/ops/CI). §5.4 LLM borderline and the §13 admin-rights self-check are OUT of scope for M7 (tracked as follow-ups).

## Global Constraints

- **Module path:** `github.com/stufently/telegram-antispam` (verbatim).
- **No host installs.** Every `go` command via `./scripts/dev.sh` in `golang:1.26`. Docker builds run via `docker build` directly.
- **`internal/ops` is a leaf for metrics:** the registry + server import only stdlib. The digest may import `internal/store` (or take a query function) and `internal/telegram` (for the `Port` interface) — declare a narrow consumer interface rather than importing concretes where practical.
- **No new Go module dependencies** for metrics (hand-rolled Prometheus text format). The digest and server use stdlib only.
- **Container runs as non-root**, SQLite on a writable volume (spec §13). Image pins base versions (no bare `latest` for the runtime unless digest-pinned).
- **Metrics/healthz/digest never block moderation:** the ops server runs in its own goroutine; a digest send is best-effort (an error is logged, never fatal); a metrics increment is a cheap locked map op.
- **Config explicit-false semantics (M3-M6 convention):** new toggles are `*bool` (nil = unset → default).
- **English only**; commit ≤50-char imperative, no trailers, one commit per task; TDD for Go tasks.

## Interfaces carried from M1–M6 (on main, do not redefine)

- `store.*DB` with `Read() *sql.DB`; `audit(incident_id, action, scope, reason, signals, created_at)` table; `Open`/`Migrate`.
- `telegram.Port` (incl. `SendAdmin(ctx, adminChat int64, AdminMessage) (int, error)`); `AdminMessage{Text, ...}`.
- `config.Config{BotToken, AdminChatID, Action, Chats, Detection, Blocklist}`; `Duration` yaml type with `.Duration()`; `applyDetectionDefaults`/`applyBlocklistDefaults` called from `Parse`.
- `cmd/tg-antispam/main.go`: `ctx, stop := signal.NotifyContext(...)` (SIGINT/SIGTERM); `b.Start(ctx)` blocks until ctx done; background goroutines (history sweeper, blocklist syncer) use `ctx`/`shutdownCtx`. `DB_PATH`/`CONFIG_PATH` env vars.
- `scripts/dev.sh` runs `go` in `golang:1.26` as uid 1002, GOPATH under `.gopath`.

---

### Task 1: Metrics registry (Prometheus text format, no dependency)

**Files:**
- Create: `internal/ops/metrics.go`
- Test: `internal/ops/metrics_test.go`

**Interfaces:**
- Produces:
  - `type Registry struct { ... }` — thread-safe (a `sync.Mutex` + maps).
  - `func NewRegistry() *Registry`.
  - `func (r *Registry) IncCounter(name string, delta float64, labels ...string)` — increments a counter identified by name + an even-length `labels` list (`k1,v1,k2,v2`); creates it at `delta` if absent. Panics only on odd label count (programmer error) — or, safer, ignore a trailing unpaired label. Choose: ignore-and-document.
  - `func (r *Registry) SetGauge(name string, value float64, labels ...string)` — sets a gauge.
  - `func (r *Registry) Write(w io.Writer)` — writes all metrics in Prometheus text-exposition format: for each metric name, a `# TYPE <name> counter|gauge` line then one sample line per label-set `name{k="v",...} value` (no labels ⇒ `name value`). Deterministic ordering (sort metric names, and sort label-set keys) so output is stable/testable.

- [x] **Step 1: Write the failing test**

```go
package ops

import (
	"strings"
	"testing"
)

func TestRegistryExposition(t *testing.T) {
	r := NewRegistry()
	r.IncCounter("updates_total", 1)
	r.IncCounter("updates_total", 2)
	r.IncCounter("incidents_total", 1, "action", "ban")
	r.SetGauge("blocklist_size", 4860000)

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	for _, want := range []string{
		"# TYPE updates_total counter",
		"updates_total 3",
		`incidents_total{action="ban"} 1`,
		"# TYPE blocklist_size gauge",
		"blocklist_size 4.86e+06", // or 4860000 — assert on whatever your formatter emits; pick one and be consistent
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition missing %q\n---\n%s", want, out)
		}
	}
}
```
(Adjust the float formatting assertion to match your chosen `strconv.FormatFloat` form — use `'g'` and assert the exact string you produce; keep integers clean, e.g. format whole numbers without a decimal.)

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** the registry. Key each metric by name; store label-sets as a map keyed by the canonical `k="v",...` string. Escape label values minimally (`\` and `"`). Format values with `strconv.FormatFloat(v, 'g', -1, 64)`.

- [x] **Step 4: Run tests incl `-race`, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/ops/metrics.go internal/ops/metrics_test.go
git commit -m "Add metrics registry"
```

---

### Task 2: Ops HTTP server (/healthz, /metrics)

**Files:**
- Create: `internal/ops/server.go`
- Test: `internal/ops/server_test.go`

**Interfaces:**
- Consumes: `Registry` (Task 1).
- Produces:
  - `type Server struct { ... }`.
  - `func NewServer(addr string, reg *Registry) *Server` — builds an `*http.Server` with a mux: `GET /healthz` → 200 `text/plain` body `ok`; `GET /metrics` → 200 `text/plain; version=0.0.4` body from `reg.Write`.
  - `func (s *Server) Run(ctx context.Context) error` — `ListenAndServe` in the calling goroutine's control: start listening, and when `ctx` is done, `Shutdown` with a short timeout. Return `http.ErrServerClosed`-swallowed nil on clean shutdown. (Pattern: launch `ListenAndServe` in an inner goroutine, `select` on `ctx.Done()` → `s.srv.Shutdown(shutdownCtx)`.)

- [x] **Step 1: Write the failing test** — use `httptest.NewServer` wrapping the same handler (factor the mux into a `func handler(reg *Registry) http.Handler` you can test directly): `GET /healthz` ⇒ 200 body "ok"; `GET /metrics` after `reg.IncCounter("x",1)` ⇒ 200 body contains "x 1". Also a test that `Run` returns promptly after ctx cancel (bind to `127.0.0.1:0`).

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** the handler factory + Server + Run (graceful shutdown).

- [x] **Step 4: Run tests, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/ops/server.go internal/ops/server_test.go
git commit -m "Add ops http server"
```

---

### Task 3: Daily digest

**Files:**
- Create: `internal/ops/digest.go`
- Test: `internal/ops/digest_test.go`

**Interfaces:**
- Consumes: a narrow `AdminSender interface { SendAdmin(ctx context.Context, adminChat int64, msg telegram.AdminMessage) (int, error) }` (satisfied by the live port); a `DigestSource interface { ActionCountsSince(ts int64) (map[string]int, error) }` (satisfied by `*store.DB` — add that method to the store in THIS task, in `internal/store/digest.go`).
- Produces:
  - `store.(*DB).ActionCountsSince(ts int64) (map[string]int, error)` — `SELECT action, COUNT(*) FROM audit WHERE created_at >= ? GROUP BY action`. Read-only.
  - `func BuildDigest(counts map[string]int, sinceHuman string) string` — a compact human summary (e.g. `Daily digest (last 24h): ban 12, delete_mute 34, mute 3 — total 49`; `No incidents in the last 24h.` when empty). Deterministic (sort actions).
  - `func SendDigest(ctx context.Context, sender AdminSender, adminChat int64, src DigestSource, now int64) error` — computes `since = now - 86400`, gets counts, builds the text, sends via `sender.SendAdmin(ctx, adminChat, telegram.AdminMessage{Text: text})`; best-effort semantics are the caller's (return the error for the caller to log).

- [x] **Step 1: Write the failing test** — `BuildDigest(map[string]int{"ban":2,"mute":1}, "last 24h")` contains "ban 2", "mute 1", "total 3"; empty map ⇒ the no-incidents line. `SendDigest` with a fake `AdminSender` + fake `DigestSource` ⇒ one SendAdmin call with the built text. Add a `store` test for `ActionCountsSince` against a temp DB (insert audit rows at known timestamps, assert the counts window).

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** the store method, `BuildDigest`, `SendDigest`.

- [x] **Step 4: Run tests, expect pass.**

- [x] **Step 5: Commit**
```bash
git add internal/ops/digest.go internal/ops/digest_test.go internal/store/digest.go internal/store/digest_test.go
git commit -m "Add daily digest"
```

---

### Task 4: Config + wire ops into main

**Files:**
- Modify: `internal/config/config.go`, `config.example.yaml`
- Modify: `cmd/tg-antispam/main.go`
- Test: `internal/config/config_test.go` (add cases)

**Interfaces:**
- Produces a `Config.Ops` block (defaults in a new `applyOpsDefaults` called from `Parse`):
  - `MetricsEnabled *bool` (yaml `metrics_enabled`) — default true.
  - `MetricsAddr string` (yaml `metrics_addr`) — default `:9090`.
  - `DigestEnabled *bool` (yaml `digest_enabled`) — default true.
  - `DigestInterval Duration` (yaml `digest_interval`) — default 24h (clamp `<= 0` to default, per M6's lesson).
- main.go wiring:
  - Build `reg := ops.NewRegistry()`; if `*cfg.Ops.MetricsEnabled` start `go func(){ if err := ops.NewServer(cfg.Ops.MetricsAddr, reg).Run(ctx); err != nil { log.Printf("ops server: %v", err) } }()`.
  - Increment counters at existing seams: in the default-handler switch bump `reg.IncCounter("updates_total", 1)` (label by kind: message/edited/callback/chat_member/reaction). Wherever a verdict is applied (the decide hook or machine), bump `reg.IncCounter("incidents_total", 1, "action", string(verdict.Action))` for actionable verdicts. Set `reg.SetGauge("blocklist_size", float64(bl.Len()))` right after the blocklist syncer's refreshes — simplest: a small ticker in main that sets it from `bl.Len()`, or skip the gauge if it complicates wiring (counters are the priority). Keep instrumentation minimal and non-invasive; do NOT thread the registry deep into `internal/detect` (pure) — instrument at the `main` wiring/handler layer and in the incident machine only if a clean seam exists (else count at the decide-hook in main).
  - If `*cfg.Ops.DigestEnabled` start a digest goroutine: a `time.Ticker(cfg.Ops.DigestInterval.Duration())` that calls `ops.SendDigest(ctx, livePort, cfg.AdminChatID, db, <now>)`; log errors; return on `ctx.Done()`. (For the timestamp, use `time.Now().Unix()` — this is app code, `time.Now` is fine here.)

- [x] **Step 1: Write the failing test** (config only) — no `ops:` block ⇒ defaults (MetricsEnabled true, `:9090`, DigestEnabled true, 24h); explicit `metrics_enabled: false` + `metrics_addr: ":1234"` honored.

- [x] **Step 2: Run test, expect failure.**

- [x] **Step 3: Implement** config + defaults + example.yaml + main wiring + minimal instrumentation.

- [x] **Step 4: Run full suite + vet + build** — `./scripts/dev.sh test ./...`, `vet ./...`, `build ./...` green.

- [x] **Step 5: Commit**
```bash
git add internal/config config.example.yaml cmd/tg-antispam/main.go
git commit -m "Wire ops server and digest"
```

---

### Task 5: Dockerfile

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

**Interfaces:** a multi-stage build:
- Builder: `FROM golang:1.26 AS build`; copy go.mod/go.sum, `go mod download`; copy source; `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X .../internal/version.Version=$(...)" -o /out/tg-antispam ./cmd/tg-antispam` (modernc.org/sqlite is pure-Go, so CGO_ENABLED=0 works).
- Runtime: a minimal non-root base (`gcr.io/distroless/static:nonroot` OR `alpine` with a created non-root user); copy the binary; `USER nonroot` (or a uid); declare a `VOLUME` for the SQLite dir; `ENTRYPOINT ["/tg-antispam"]`. Document required env (`CONFIG_PATH`, `DB_PATH`, `BOT_TOKEN` if used).
- `.dockerignore` excludes `.git`, `.gopath`, `.worktrees`, `.superpowers`, `docs`, test artifacts.

- [x] **Step 1: Write the Dockerfile + .dockerignore** per above (there is no unit test; the gate is a successful build).

- [x] **Step 2: Verify the image builds** — `docker build -t tg-antispam:m7 .` succeeds (run from the repo root; this compiles the whole module). Confirm the binary runs `--help`/version if the program supports it, else that the image is created.

- [x] **Step 3: Confirm non-root** — `docker run --rm tg-antispam:m7 id` (or inspect) shows a non-root user, OR the Dockerfile's `USER` directive is a non-root uid.

- [x] **Step 4: Commit**
```bash
git add Dockerfile .dockerignore
git commit -m "Add Dockerfile"
```

---

### Task 6: docker-compose + Helm chart

**Files:**
- Create: `docker-compose.yml`
- Create: `deploy/helm/tg-antispam/Chart.yaml`, `values.yaml`, `templates/deployment.yaml`, `templates/configmap.yaml` (+ `_helpers.tpl` if needed)

**Interfaces:**
- `docker-compose.yml`: one service `tg-antispam` building `.` (or using the GHCR image), running as `user: "1002:1002"` (matches the repo convention), mounting a named volume for `DB_PATH`, mounting a config file read-only, env for `CONFIG_PATH`/`DB_PATH`, and exposing the metrics port. Restart policy `unless-stopped`.
- Helm chart: a minimal Deployment (non-root securityContext, a PVC or emptyDir for SQLite, a ConfigMap for the YAML config, the metrics port, liveness/readiness probes hitting `/healthz`), `values.yaml` for image/tag/resources/config, `Chart.yaml` with apiVersion v2.

- [x] **Step 1: Write docker-compose.yml.**

- [x] **Step 2: Validate compose** — `docker compose config` (parses/normalizes) succeeds. (If `docker compose` is unavailable, at minimum `python3 -c "import yaml,sys; yaml.safe_load(open('docker-compose.yml'))"`.)

- [x] **Step 3: Write the Helm chart files.**

- [x] **Step 4: Validate the chart** — `helm lint deploy/helm/tg-antispam` if helm is available; otherwise validate each template renders as YAML (`python3` yaml.safe_load on the non-templated files, and a structural review of the templates). Note in the report which validation ran.

- [x] **Step 5: Commit**
```bash
git add docker-compose.yml deploy/helm
git commit -m "Add compose and helm chart"
```

---

### Task 7: CI (GitHub Actions + golangci-lint + goreleaser)

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.golangci.yml`
- Create: `.goreleaser.yml`

**Interfaces:**
- `.github/workflows/ci.yml`: on push/PR — a `test` job (Go 1.26, `go test -race ./...`, `go vet ./...`), a `lint` job (golangci-lint-action), and an `image` job (on tags or main) that builds and pushes to GHCR (`docker/build-push-action`, `permissions: packages: write`, login with `GITHUB_TOKEN`). Use pinned action versions.
- `.golangci.yml`: enable a sane default linter set (govet, staticcheck, errcheck, ineffassign, gofmt, etc.) — nothing that would fail the existing clean codebase; scope it so the current tree passes.
- `.goreleaser.yml`: build the `./cmd/tg-antispam` binary for linux/amd64+arm64, a Docker image, and a GitHub release; `CGO_ENABLED=0`.

- [x] **Step 1: Write ci.yml, .golangci.yml, .goreleaser.yml.**

- [x] **Step 2: Validate YAML** — all three parse (`python3 yaml.safe_load`). Confirm the workflow references correct paths (`./cmd/tg-antispam`, Go 1.26) and the goreleaser config is v2-compatible (`version: 2`). If `golangci-lint` is available in Docker, optionally run it against the tree to confirm the config passes; otherwise structurally review the enabled linters against the codebase and note that CI will be the first live run.

- [x] **Step 3: Sanity-check the linter set doesn't obviously break** — the codebase is already gofmt/vet clean; ensure `.golangci.yml` doesn't enable something that fails a clean, idiomatic tree (e.g. avoid `exhaustruct`, `wsl`, `nlreturn` and other opinionated linters for an existing codebase).

- [x] **Step 4: Commit**
```bash
git add .github/workflows/ci.yml .golangci.yml .goreleaser.yml
git commit -m "Add CI pipeline"
```

---

## Milestone M7 Definition of Done

- `./scripts/dev.sh test ./...` green (with `-race` on `ops`); `build`/`vet` clean; new Go files gofmt-clean.
- `internal/ops` exposes a dependency-free Prometheus `/metrics` and a `/healthz` endpoint served on a configurable address, started under the shutdown context; key counters (updates, incidents-by-action) are incremented at the wiring layer without polluting the pure `internal/detect`.
- A daily digest of the last 24h of `audit` actions is sent to the admin chat on a configurable interval, best-effort (never fatal).
- `docker build .` produces a non-root image with the pure-Go binary and a SQLite volume; `docker compose config` validates; a Helm chart lints/validates with `/healthz` probes.
- CI (`.github/workflows/ci.yml`) runs `go test -race`, `go vet`, `golangci-lint`, and builds/pushes a GHCR image; `.golangci.yml` passes the current clean tree; `.goreleaser.yml` is valid v2.
- All new config keys documented in `config.example.yaml` with explicit-false honored.

**Deferred (tracked):**
- **Admin-rights self-check** (spec §13) — verify the bot's `can_delete_messages`/`can_restrict_members` per chat on startup and `my_chat_member`, warn on missing; detect native Aggressive Anti-Spam. Needs new `GetMe`/`GetChatMember` port methods — its own follow-up.
- **LLM borderline (§5.4)** — two-provider consensus over the Bayes borderline band; paid opt-in APIs. Its own milestone (M8).
- **blocklist_size gauge** wired only if a clean seam exists; otherwise counters ship first.
