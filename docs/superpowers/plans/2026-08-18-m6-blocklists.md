# telegram-antispam M6 — LOLS/CAS Blocklists — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the LOLS+CAS blocklist stage (spec §5.3) — a background-synced in-memory mirror of the union of the two largest public Telegram spammer-ID lists, checked as a cheap authoritative hard signal in the detection cascade — so the bot bans known spammers on sight, fail-open (a blocklist outage never blocks a chat).

**Architecture:** A new `internal/blocklist` package owns all I/O and state: an immutable sorted `[]int64` snapshot with binary-search membership, swapped atomically on refresh; an HTTP fetcher that streams newline-delimited id lists; and a background syncer (bootstrap + periodic full refresh of both sources + hourly LOLS delta merge) that keeps the last-good snapshot on any fetch error. `internal/detect` stays pure: the cascade gains a blocklist stage gated behind a consumer-declared `BlocklistSource` interface (`Listed(userID int64) bool`), which the blocklist store satisfies. `main` constructs the store, starts the syncer under the shutdown context, and injects it into the cascade.

**Tech Stack:** Go 1.24+, stdlib only (`net/http`, `bufio`, `sort`, `strconv`, `sync/atomic`), `modernc.org/sqlite` unaffected. All build/test in Docker (`golang:1.26`) via `./scripts/dev.sh`.

**Spec:** `docs/superpowers/specs/2026-08-18-telegram-antispam-design.md` §5.3 (blocklists). **API facts (verified live):** `.superpowers/blocklist-api-facts.md`.

## Global Constraints

- **Module path:** `github.com/stufently/telegram-antispam` (verbatim).
- **No host installs.** Every `go` command via `./scripts/dev.sh` in `golang:1.26`.
- **Detection stays pure (spec §3.1):** `internal/detect` imports only `domain` + stdlib + `x/text`. The blocklist membership check arrives via a `BlocklistSource` interface implemented in `internal/blocklist`; detect never does I/O.
- **`internal/blocklist` owns I/O:** it may import `net/http`/`bufio`/stdlib; it must NOT import `internal/detect`, `internal/telegram`, or the bot library. It is a leaf package (detect declares the interface it satisfies; blocklist does not import detect).
- **Fail-open (spec §5.3):** the snapshot starts empty; a lookup on an empty/not-yet-loaded snapshot returns `false` (not listed). A failed fetch NEVER clears the last-good snapshot. A blocklist outage must never block a chat, delay moderation, or panic.
- **No per-message network:** membership is a local binary search; the per-user LOLS/CAS APIs are NOT called in the hot path (out of v1 scope).
- **Sources (defaults, from the API facts — config-overridable):** LOLS full `https://lols.bot/spam/banlist.txt`, LOLS 1h delta `https://lols.bot/spam/banlist-1h.txt`, CAS full `https://api.cas.chat/export.csv`. All are plain UTF-8, one int64 user_id per line, no header.
- **Config explicit-false semantics (M3-M5 convention):** the enable toggle is `*bool` (nil = unset → default); an explicit `false` is honored.
- **English only**; commit ≤50-char imperative, no trailers, one commit per task; TDD.

## Interfaces carried from M1–M5 (on main, do not redefine)

- `detect.Cascade{Trust,Hist,Rules,Behavior,TrustThreshold,DefaultAction,DefaultScope,Bayes*,Admins,FakeAdmin}` + `Decide(m,edited)`. Current `Decide` order: **admin-immunity gate (§4)** → Rules → FakeAdmin → Behavior → Bayes. `IsTrusted(src,chat,user,threshold)`; `actionable(sig)` builds the verdict with `Reason=sig.Name`.
- `domain.Message{ChatID, Sender{UserID}, ...}`, `domain.Signal{Name,Detail}`, `domain.Verdict`, `domain.Action`.
- `config.Config{...,Detection}`; `Detection` fields defaulted in `applyDetectionDefaults`; `config.example.yaml` documents keys. Config has a top-level shape — add a sibling `Blocklist` block (see Task 5).
- `cmd/tg-antispam/main.go`: builds `livePort`, `cascade := detect.Cascade{...}`, wires handlers; has a `shutdownCtx` (process-shutdown-aware context) and starts background goroutines (e.g. the history sweeper) that observe it.

---

### Task 1: Sorted-set snapshot

**Files:**
- Create: `internal/blocklist/set.go`
- Test: `internal/blocklist/set.go` → `internal/blocklist/set_test.go`

**Interfaces:**
- Produces:
  - `type Set struct { ids []int64 }` — holds a SORTED, DEDUPED ascending `[]int64`. Immutable once built.
  - `func BuildSet(sources ...[]int64) *Set` — concatenates all input slices, sorts ascending, dedups, returns a `*Set`. Input slices may be unsorted and overlap. An empty/no-input build returns a non-nil `*Set` with `Len()==0`.
  - `func (s *Set) Contains(id int64) bool` — binary search (`sort.Search`); a nil `*Set` receiver returns false (fail-open).
  - `func (s *Set) Len() int` — count (0 for nil receiver).

- [ ] **Step 1: Write the failing test**

```go
package blocklist

import "testing"

func TestBuildSetAndContains(t *testing.T) {
	s := BuildSet([]int64{5, 1, 3}, []int64{3, 9})
	if s.Len() != 4 { // {1,3,5,9} deduped
		t.Fatalf("len=%d want 4", s.Len())
	}
	for _, id := range []int64{1, 3, 5, 9} {
		if !s.Contains(id) {
			t.Errorf("Contains(%d)=false want true", id)
		}
	}
	for _, id := range []int64{0, 2, 4, 10} {
		if s.Contains(id) {
			t.Errorf("Contains(%d)=true want false", id)
		}
	}
	var nilSet *Set
	if nilSet.Contains(7) || nilSet.Len() != 0 {
		t.Fatal("nil set must be empty and contain nothing (fail-open)")
	}
	if BuildSet().Len() != 0 {
		t.Fatal("empty build must be len 0")
	}
}
```

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `Set`, `BuildSet` (append all, `sort.Slice`/`slices.Sort`, dedup in-place), `Contains` (`sort.Search` + bounds+equality, nil-guard), `Len` (nil-guard).

- [ ] **Step 4: Run test, expect pass** (`./scripts/dev.sh test ./internal/blocklist/...`).

- [ ] **Step 5: Commit**
```bash
git add internal/blocklist/set.go internal/blocklist/set_test.go
git commit -m "Add sorted-set blocklist snapshot"
```

---

### Task 2: Blocklist store with atomic snapshot

**Files:**
- Create: `internal/blocklist/blocklist.go`
- Test: `internal/blocklist/blocklist_test.go`

**Interfaces:**
- Consumes: `Set`, `BuildSet` (Task 1).
- Produces:
  - `type Blocklist struct { snap atomic.Pointer[Set] }` (plus fields added in Task 4 for syncing — keep this task to the snapshot holder + lookup).
  - `func New() *Blocklist` — returns a store whose snapshot is empty (a non-nil `*Set` with Len 0, so `Listed` is safe before the first load).
  - `func (b *Blocklist) Listed(userID int64) bool` — `b.snap.Load().Contains(userID)`; a zero userID returns false; fail-open (empty snapshot ⇒ false). This is the method the cascade's `BlocklistSource` interface needs.
  - `func (b *Blocklist) Swap(s *Set)` — atomically replaces the snapshot (used by the syncer).
  - `func (b *Blocklist) Len() int` — current snapshot size (for logging/metrics).

- [ ] **Step 1: Write the failing test** — `New()`; `Listed(5)` is false (empty). `Swap(BuildSet([]int64{5,9}))`; `Listed(5)` true, `Listed(6)` false, `Listed(0)` false; `Len()==2`. Confirm concurrent `Listed` + `Swap` is race-free (a short goroutine loop calling Listed while another Swaps — run under `-race`).

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** using `sync/atomic` `atomic.Pointer[Set]`. `New()` stores an empty `BuildSet()`.

- [ ] **Step 4: Run tests incl. `-race`** (`./scripts/dev.sh test -race ./internal/blocklist/...`).

- [ ] **Step 5: Commit**
```bash
git add internal/blocklist/blocklist.go internal/blocklist/blocklist_test.go
git commit -m "Add atomic blocklist store"
```

---

### Task 3: ID list fetcher/parser

**Files:**
- Create: `internal/blocklist/fetch.go`
- Test: `internal/blocklist/fetch_test.go`

**Interfaces:**
- Produces:
  - `func ParseIDs(r io.Reader) ([]int64, error)` — scans line by line (`bufio.Scanner` with an enlarged buffer, since files are large but lines are short), trims whitespace, SKIPS blank lines and any line that isn't a base-10 int64 (tolerant — a stray header or comment must not fail the whole parse), returns the parsed ids. Returns the scanner's error only for a genuine read error, never for an unparseable line.
  - `func FetchIDs(ctx context.Context, client *http.Client, url string) ([]int64, error)` — GET `url` with the context; on non-2xx returns an error; on success streams the body through `ParseIDs`. Caller supplies the `*http.Client` (so the timeout is injectable/testable).

- [ ] **Step 1: Write the failing test** — `ParseIDs(strings.NewReader("1\n2\n\n  3 \nnotanid\n4\n"))` ⇒ `[1,2,3,4]`, no error. For `FetchIDs`, stand up an `httptest.NewServer` returning `"5\n6\n7\n"` ⇒ `[5,6,7]`; a server returning 500 ⇒ error; a canceled context ⇒ error.

```go
func TestParseIDsTolerant(t *testing.T) {
	got, err := ParseIDs(strings.NewReader("1\n2\n\n  3 \nnotanid\n4\n"))
	if err != nil { t.Fatal(err) }
	want := []int64{1, 2, 3, 4}
	if !reflect.DeepEqual(got, want) { t.Fatalf("got %v want %v", got, want) }
}
```

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `ParseIDs` (bufio.Scanner, `scanner.Buffer(make([]byte,0,64*1024), 1024*1024)`, `strings.TrimSpace`, `strconv.ParseInt(line,10,64)` skipping errors) and `FetchIDs` (http.NewRequestWithContext, GET, status check, `defer resp.Body.Close()`, `ParseIDs(resp.Body)`).

- [ ] **Step 4: Run tests, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/blocklist/fetch.go internal/blocklist/fetch_test.go
git commit -m "Add blocklist id fetcher"
```

---

### Task 4: Syncer (bootstrap + full/delta refresh, fail-open)

**Files:**
- Modify: `internal/blocklist/blocklist.go` (add syncer fields/config)
- Create: `internal/blocklist/sync.go`
- Test: `internal/blocklist/sync_test.go`

**Interfaces:**
- Consumes: `FetchIDs` (Task 3), `BuildSet`/`Set` (Task 1), `Blocklist.Swap`/`Len` (Task 2).
- Produces:
  - `type Config struct { LolsFullURL, LolsDeltaURL, CasFullURL string; FullInterval, DeltaInterval, HTTPTimeout time.Duration }`.
  - `func (b *Blocklist) fetch(ctx) ([]int64, error)` is NOT the shape — instead inject the fetcher for testability: `type fetchFn func(ctx context.Context, url string) ([]int64, error)`. Add unexported fields to `Blocklist`: `cfg Config`, `fetch fetchFn`, `client *http.Client`. Add `func NewWithConfig(cfg Config) *Blocklist` that sets `client` (timeout `cfg.HTTPTimeout`) and `fetch = func(ctx,url){ return FetchIDs(ctx, b.client, url) }`; keep `New()` for the empty/no-sync store used by tests that only need lookups.
  - `func (b *Blocklist) RefreshFull(ctx context.Context) error` — fetch LOLS full + CAS full; if BOTH fail, return an error and DO NOT swap (keep last-good); if at least one succeeds, `Swap(BuildSet(the successful lists...))` and return nil (a partial refresh is better than none, and still fail-open). Log which source failed via a returned/loggable detail is out of scope — return the combined error for the caller to log.
  - `func (b *Blocklist) RefreshDelta(ctx context.Context) error` — fetch LOLS 1h delta; on success, merge into the CURRENT snapshot: `Swap(BuildSet(current.ids, delta))` (BuildSet dedups); on failure return the error and keep last-good.
  - `func (b *Blocklist) Run(ctx context.Context)` — the background loop: do an initial `RefreshFull(ctx)` (bootstrap; log on error but keep running — fail-open), then two tickers (`FullInterval`, `DeltaInterval`); on each full tick call `RefreshFull`, on each delta tick call `RefreshDelta`; return when `ctx.Done()`. Never panics; a refresh error is logged, not fatal.
  - To let `RefreshDelta` read the current snapshot's ids for the merge, add `func (s *Set) IDs() []int64` (returns the internal slice; document it as read-only — callers must not mutate) OR have `Blocklist` expose the current `*Set`. Keep it simple: add `func (b *Blocklist) current() *Set { return b.snap.Load() }` (unexported) and `Set.IDs()`.

- [ ] **Step 1: Write the failing test** — inject a fake `fetch` by constructing a `Blocklist` with the unexported fields set in-package (the test is in package `blocklist`). Script the fake to return per-URL id lists. `RefreshFull` with both sources returning ids ⇒ snapshot is their union; one source erroring ⇒ snapshot is the other's ids (partial, no error since one succeeded); BOTH erroring ⇒ returns error AND the prior snapshot is unchanged (fail-open — set a prior snapshot via Swap, then assert it survives). `RefreshDelta` returning new ids ⇒ they're added to the existing snapshot (union), existing ids retained; delta fetch error ⇒ snapshot unchanged, error returned.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** `Config`, `NewWithConfig`, `RefreshFull`/`RefreshDelta` (call `b.fetch` per URL, combine results, `errors.Join` the failures), `Run` (bootstrap + tickers + ctx). `Set.IDs()`.

- [ ] **Step 4: Run tests incl. `-race`, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/blocklist
git commit -m "Add blocklist syncer"
```

---

### Task 5: Blocklist config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`
- Test: `internal/config/config_test.go` (add cases)

**Interfaces:**
- Produces a new top-level `Config.Blocklist` block (sibling of `Detection`), with defaults applied in `Parse` (add `applyBlocklistDefaults()` called next to `applyDetectionDefaults()`):
  - `Enabled *bool` (yaml `enabled`) — default true.
  - `LolsFullURL string` (yaml `lols_full_url`) — default `https://lols.bot/spam/banlist.txt`.
  - `LolsDeltaURL string` (yaml `lols_delta_url`) — default `https://lols.bot/spam/banlist-1h.txt`.
  - `CasFullURL string` (yaml `cas_full_url`) — default `https://api.cas.chat/export.csv`.
  - `FullRefresh Duration` (yaml `full_refresh`) — default 6h. (Reuse the existing `Duration` yaml type used by `Detection.Behavior` windows — check its type name in config.go and reuse it; do not invent a new duration type.)
  - `DeltaRefresh Duration` (yaml `delta_refresh`) — default 1h.
  - `HTTPTimeout Duration` (yaml `http_timeout`) — default 30s.
  Each string/duration defaults only when its zero value (empty / 0); `Enabled` defaults only when nil (explicit `false` honored). Document all keys in `config.example.yaml` under a new `blocklist:` section.

- [ ] **Step 1: Write the failing test** — parse YAML with no `blocklist:` block ⇒ all defaults applied (Enabled true, the three real URLs, 6h/1h/30s). Parse YAML with `blocklist:\n  enabled: false\n  full_refresh: 12h` ⇒ enabled false honored, full_refresh 12h honored, other fields still defaulted.

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the struct + `applyBlocklistDefaults` + example.yaml.

- [ ] **Step 4: Run tests, expect pass.**

- [ ] **Step 5: Commit**
```bash
git add internal/config config.example.yaml
git commit -m "Add blocklist config"
```

---

### Task 6: Cascade blocklist stage

**Files:**
- Modify: `internal/detect/cascade.go`
- Test: `internal/detect/cascade_test.go` (add cases)

**Interfaces:**
- Produces:
  - `type BlocklistSource interface { Listed(userID int64) bool }` (declared in package detect; `*blocklist.Blocklist` satisfies it).
  - `Cascade` gains `Blocklist BlocklistSource` and `BlocklistEnabled bool`.
  - In `Decide`, add the blocklist stage AFTER the §4 admin-immunity gate and BEFORE `Rules.Check`, gated on non-trusted senders (a global-banlist hit is authoritative but established local members are exempt, consistent with the trust-gate): 
    ```go
    if c.BlocklistEnabled && !trusted && c.Blocklist != nil && c.Blocklist.Listed(m.Sender.UserID) {
        return c.actionable(domain.Signal{Name: "blocklist"}), true
    }
    ```
    Note `trusted` is computed once near the top; ensure the blocklist stage sees it (move the `trusted := IsTrusted(...)` line above this stage if needed, but keep it AFTER the admin-immunity gate). A nil `Blocklist` or `!BlocklistEnabled` skips the stage (existing tests unaffected — zero-value false).

- [ ] **Step 1: Write the failing test** — a `Cascade` with a fake `BlocklistSource` (`type fakeBlocklist struct{ ids map[int64]bool }` with `Listed(id) bool`), `BlocklistEnabled:true`, untrusted sender whose UserID is listed ⇒ `Decide` returns actionable with `Reason=="blocklist"`. A TRUSTED sender with the same listed id ⇒ not actionable (blocklist skipped for trusted). An admin (in the AdminSource) who is also listed ⇒ not actionable (admin-immunity gate wins, runs first). A non-listed untrusted sender ⇒ falls through (not actionable on this stage).

- [ ] **Step 2: Run test, expect failure.**

- [ ] **Step 3: Implement** the two fields + the stage in the correct position; adjust the `trusted` computation placement so both the admin gate (which does not need `trusted`) and this stage are correct.

- [ ] **Step 4: Run tests + vet, expect pass** — all existing cascade tests still green.

- [ ] **Step 5: Commit**
```bash
git add internal/detect/cascade.go internal/detect/cascade_test.go
git commit -m "Add blocklist stage to cascade"
```

---

### Task 7: Wire blocklist into main

**Files:**
- Modify: `cmd/tg-antispam/main.go`
- Test: build + full suite (no new unit test file required; wiring is covered by build + package tests)

**Interfaces:**
- Produces main.go wiring:
  - After `livePort`/`db` are built, if `*cfg.Blocklist.Enabled`: construct `bl := blocklist.NewWithConfig(blocklist.Config{LolsFullURL: cfg.Blocklist.LolsFullURL, LolsDeltaURL: cfg.Blocklist.LolsDeltaURL, CasFullURL: cfg.Blocklist.CasFullURL, FullInterval: cfg.Blocklist.FullRefresh.Duration(), DeltaInterval: cfg.Blocklist.DeltaRefresh.Duration(), HTTPTimeout: cfg.Blocklist.HTTPTimeout.Duration()})` and start its syncer: `go bl.Run(shutdownCtx)` (shutdown-aware, like the existing history sweeper goroutine). Set `cascade.Blocklist = bl` and `cascade.BlocklistEnabled = true`. If blocklist is disabled, leave `cascade.Blocklist` nil / `BlocklistEnabled` false (the stage no-ops).
  - Add the `internal/blocklist` import. Use the `Duration.Duration()` accessor consistent with how `Detection.Behavior.*Window.Duration()` is already used.
  - Log the initial snapshot size after a moment is NOT required; the syncer logs its own refresh outcomes (add a `log.Printf` inside RefreshFull/Run on success/failure in Task 4 if not already — keep it minimal).

- [ ] **Step 1:** (No failing unit test — this is wiring.) Confirm the cascade literal and the new goroutine compile against the real types.

- [ ] **Step 2: Build to verify the wiring** — `./scripts/dev.sh build ./...` MUST pass.

- [ ] **Step 3: Implement** the construction, goroutine, cascade fields, import.

- [ ] **Step 4: Run full suite + vet + build** — `./scripts/dev.sh test ./...`, `vet ./...`, `build ./...` all green; `-race` on `./internal/blocklist/...`.

- [ ] **Step 5: Commit**
```bash
git add cmd/tg-antispam/main.go
git commit -m "Wire blocklist into main"
```

---

## Milestone M6 Definition of Done

- `./scripts/dev.sh test ./...` green (with `-race` on `blocklist`, `detect`); `build`/`vet` clean; new/touched files gofmt-clean.
- `internal/blocklist` holds the union of the LOLS full + CAS export lists as an atomically-swapped sorted `[]int64`; membership is a binary search; the LOLS 1h delta is merged on a shorter interval; a full refresh runs on a longer interval.
- Fail-open proven by tests: an empty/not-yet-loaded snapshot returns not-listed; a failed fetch keeps the last-good snapshot; both-source failure never clears the snapshot; nothing panics.
- The cascade runs the blocklist stage for non-trusted, non-admin senders right after the §4 admin-immunity gate and before hard rules; a hit ⇒ actionable `blocklist` verdict at the configured default action/scope; trusted senders and admins skip it.
- Config-gated with documented `config.example.yaml` keys (enable + three source URLs + three intervals) and explicit-false honored; the real default URLs match `.superpowers/blocklist-api-facts.md`.
- Detection stays pure (`internal/detect` imports only domain+stdlib+x/text); `internal/blocklist` is a leaf (no detect/telegram/library import); only `cmd` connects them; the syncer goroutine observes the shutdown context.

**Deferred (tracked):**
- Per-user LOLS/CAS API fallback (`/account`, `/check`) for ids not in the mirror — out of v1 scope; the mirror is authoritative for M6.
- Persisting the snapshot to disk for fast cold-start (currently rebuilt from the network on boot; fail-open means an empty window until the first fetch completes).
- `scammers` list and `spam_factor`/`offenses` signals from the LOLS per-user API — a later enrichment.
