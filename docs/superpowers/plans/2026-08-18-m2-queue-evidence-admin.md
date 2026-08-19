# telegram-antispam M2 — Outbound Queue, Evidence, Admin Chat — Implementation Plan

> **Status: ✅ Implemented, reviewed, and merged to `main`.** Every step below is complete; the whole-branch review passed. Checkboxes are ticked for historical record.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Turn the M1 dry-run spine into a bot that actually acts: a real go-telegram/bot-backed Telegram port behind a rate-limited priority outbound queue, album-aware evidence copied to the admin chat before any action, and a four-button admin flow with per-callback RBAC — with the incident machine finally invoked from the update handler.

**Architecture:** M1 left `telegram.Port` faked and the incident `Machine` wired-but-uninvoked. M2 supplies the real port (calling the library through the outbound queue so every side effect is rate-limited and retried), buffers album parts before building an incident, invokes the machine from `OnMessage`, and adds the admin callback handler. Detection is still not here (M3) — every message that reaches the machine in M2 is driven by an explicit test verdict or the trivial "flag to admin in dry-run" path, so M2 is exercised without real classifiers.

**Tech Stack:** Go 1.24+, `github.com/go-telegram/bot` v1.23.0, `modernc.org/sqlite`, `golang.org/x/time/rate` (token-bucket limiter). All build/test in Docker (`golang:1.26`) via `./scripts/dev.sh`.

**Spec:** `docs/superpowers/specs/2026-08-18-telegram-antispam-design.md` (implements build-order items 4–5 of spec §15, plus the M2-relevant deferred items).

## Global Constraints

- **Module path:** `github.com/stufently/telegram-antispam` (verbatim in every import).
- **No host installs.** Every `go` command runs in `golang:1.26` via `./scripts/dev.sh`. Never run `go` on the host.
- **Rate limits (spec §11):** a global token bucket AND a per-destination-chat bucket. Queue is **priority-ordered**: delete/ban above notifications above TTL cleanup. Honor `429` `Retry-After`. `deleteMessages` in batches of ≤100 (spec §7, §11).
- **Evidence before action (spec §7):** the machine copies evidence before any destructive call. `banChatMember` in a supergroup deletes prior messages, so this ordering is an API requirement, not style.
- **Album buffering (spec §7):** buffer parts on `(chat_id, media_group_id)` ~700ms; a late part of a decided album is attached and deleted.
- **Mute bounds (spec §6.4):** a restrict `until_date` must be within `[30s, 366d]` of now, else Telegram makes it permanent. Pass `0` only where the port maps it to "forever by omission" deliberately; for temporary mutes always pass an in-range absolute unix time.
- **`deleteMessage` 48h limit (spec §7):** deletes only work within 48h; a failed delete is recorded, not retried forever.
- **RBAC (spec §8):** a callback presser must be an admin of the *source* chat or a configured global operator; checking the admin chat id alone is insufficient.
- **Message identity `(chat_id, message_id)`; never a text hash.** Update dedup via `update_id`.
- **English only** in code, comments, identifiers, commit messages.
- **Commit style:** ≤50-char imperative subject, no Co-Authored-By, no signatures, one commit per task.
- **TDD:** failing test first, watch it fail, minimal code, watch it pass, commit.

## Interfaces carried from M1 (already on main, do not redefine)

- `domain`: `Message{ChatID,MessageID,ThreadID,MediaGroupID,Sender,Text,Date,IsAutomaticForward,LinkedChatID}`, `Sender{Kind,UserID,SenderChatID,Username,DisplayName}`, `Verdict{Action,Scope,Confidence,Signals,Reason}`, `Incident{ChatID,MessageIDs,ThreadID,Sender,Verdict,State,DryRun,AdminMessageIDs}`, `Action`/`Scope`/`IncidentState`/`SenderKind` enums.
- `telegram`: `Port` interface (`CopyMessages`, `DeleteMessages`, `BanMember`, `RestrictMember`, `SendAdmin`), `Perms{CanSend bool}`, `Member{UserID,Status,Username,DisplayName}`, `AdminMessage{Text,IncidentKey,SourceChatID,CopiedFromChatID,CopyMessageIDs}`, `ToDomainMessage`, `Sequencer` (`NewSequencer`/`Submit`/`Wait`), `RegisteredChat`, `ImmuneSender`, `Handler`/`NewHandler`/`OnMessage`, `IncidentMachine` interface (`Handle(ctx, domain.Incident) error`).
- `incident`: `Machine`/`New(port, repo, adminChatID)`/`Handle`, `Repo` interface.
- `store`: `*DB` with `Open/Migrate/Close`, `MarkUpdateSeen`, `RegisterChat`, `UpsertChat`, `GetChat`, `DisableChat`, `AddAlias`, `ResolveChat`, `InsertPending`, `SetIncidentState`, `GetIncidentState`, `AddEvidence`, `ChatRow`, `b2i`, test helper `newMigrated`.
- `config`: `Config{BotToken,AdminChatID,Action,Chats}`, `Store`/`NewStore`/`Current`/`Swap`/`Watch`, `Load/Parse/Validate`.

---

### Task 1: Extend the Telegram port for M2 methods

**Files:**
- Modify: `internal/telegram/port.go`
- Modify: `internal/telegram/fake/fake.go`
- Test: `internal/telegram/fake/fake_test.go`

**Interfaces:**
- Consumes: existing `Port`, `Member`, `Perms`.
- Produces: `Port` gains `BanSenderChat(ctx, chat, senderChat int64) error`, `GetChatAdministrators(ctx, chat int64) ([]Member, error)`, `AnswerCallback(ctx, callbackID, text string) error`, `EditAdminMarkup(ctx, adminChat int64, messageID int, buttons [][]Button) error`. New type `Button{Text, Data string}`. The `fake` implements all of them and records calls; add knobs `Admins []Member` (returned by `GetChatAdministrators`) and `BanSenderErr error`.

- [x] **Step 1: Write the failing test** — extend `fake_test.go`:
```go
func TestFakeM2Methods(t *testing.T) {
	f := New()
	f.Admins = []telegram.Member{{UserID: 5, Status: "administrator"}}
	ctx := context.Background()
	if err := f.BanSenderChat(ctx, -100123, -100888); err != nil {
		t.Fatal(err)
	}
	admins, err := f.GetChatAdministrators(ctx, -100123)
	if err != nil || len(admins) != 1 || admins[0].UserID != 5 {
		t.Fatalf("admins=%v err=%v", admins, err)
	}
	if err := f.AnswerCallback(ctx, "cb1", "done"); err != nil {
		t.Fatal(err)
	}
	if err := f.EditAdminMarkup(ctx, 999, 7, [][]telegram.Button{{{Text: "x", Data: "d"}}}); err != nil {
		t.Fatal(err)
	}
	got := f.Calls()
	want := []string{"BanSenderChat", "GetChatAdministrators", "AnswerCallback", "EditAdminMarkup"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d=%q want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}
```

- [x] **Step 2: Run test, expect failure** — `make test` → undefined methods/type.

- [x] **Step 3: Extend the port** — append to `internal/telegram/port.go`:
```go
// Button is one inline keyboard button (text + opaque callback data ≤64 bytes).
type Button struct {
	Text string
	Data string
}
```
and add these methods to the `Port` interface:
```go
	BanSenderChat(ctx context.Context, chat, senderChat int64) error
	GetChatAdministrators(ctx context.Context, chat int64) ([]Member, error)
	AnswerCallback(ctx context.Context, callbackID, text string) error
	EditAdminMarkup(ctx context.Context, adminChat int64, messageID int, buttons [][]Button) error
```

- [x] **Step 4: Extend the fake** — append to `internal/telegram/fake/fake.go`:
```go
func (f *Fake) BanSenderChat(_ context.Context, _, _ int64) error {
	f.log("BanSenderChat")
	return f.BanSenderErr
}

func (f *Fake) GetChatAdministrators(_ context.Context, _ int64) ([]telegram.Member, error) {
	f.log("GetChatAdministrators")
	return f.Admins, nil
}

func (f *Fake) AnswerCallback(_ context.Context, _, _ string) error {
	f.log("AnswerCallback")
	return nil
}

func (f *Fake) EditAdminMarkup(_ context.Context, _ int64, _ int, _ [][]telegram.Button) error {
	f.log("EditAdminMarkup")
	return nil
}
```
and add the knob fields to the `Fake` struct:
```go
	Admins      []telegram.Member
	BanSenderErr error
```

- [x] **Step 5: Run test, expect pass** — `make test`.

- [x] **Step 6: Commit**
```bash
git add internal/telegram
git commit -m "Extend telegram port for M2 methods"
```

---

### Task 2: Outbound queue — priority ordering

**Files:**
- Create: `internal/queue/queue.go`
- Test: `internal/queue/queue_test.go`

**Interfaces:**
- Consumes: nothing beyond stdlib.
- Produces:
  - `type Priority int` with `PrioHigh Priority = 0` (delete/ban), `PrioNormal Priority = 1` (notifications), `PrioLow Priority = 2` (TTL cleanup).
  - `type Job struct { Priority Priority; Run func(ctx context.Context) error }`.
  - `type Queue struct { ... }`, `New() *Queue`, `func (q *Queue) Push(j Job)`, `func (q *Queue) pop() (Job, bool)` (lowest Priority number first; FIFO within a priority), `func (q *Queue) Len() int`.

- [x] **Step 1: Write the failing test**
```go
package queue

import (
	"context"
	"testing"
)

func TestPopOrdersByPriorityThenFIFO(t *testing.T) {
	q := New()
	mk := func(p Priority, tag string) Job {
		return Job{Priority: p, Run: func(context.Context) error { return nil }, tag: tag}
	}
	q.Push(mk(PrioLow, "low1"))
	q.Push(mk(PrioHigh, "high1"))
	q.Push(mk(PrioNormal, "norm1"))
	q.Push(mk(PrioHigh, "high2"))
	var order []string
	for {
		j, ok := q.pop()
		if !ok {
			break
		}
		order = append(order, j.tag)
	}
	want := []string{"high1", "high2", "norm1", "low1"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v want %v", order, want)
		}
	}
}
```

- [x] **Step 2: Run test, expect failure** — undefined.

- [x] **Step 3: Implement**
```go
// Package queue is the outbound work queue: priority-ordered, rate-limited,
// retrying. All Telegram side effects flow through it (spec §11).
package queue

import (
	"container/heap"
	"context"
	"sync"
)

type Priority int

const (
	PrioHigh   Priority = 0 // delete / ban
	PrioNormal Priority = 1 // notifications
	PrioLow    Priority = 2 // TTL cleanup
)

// Job is one unit of outbound work.
type Job struct {
	Priority Priority
	Run      func(ctx context.Context) error
	tag      string // test-only label
	seq      uint64 // FIFO tiebreaker within a priority
}

type pqueue []Job

func (p pqueue) Len() int { return len(p) }
func (p pqueue) Less(i, j int) bool {
	if p[i].Priority != p[j].Priority {
		return p[i].Priority < p[j].Priority
	}
	return p[i].seq < p[j].seq
}
func (p pqueue) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p *pqueue) Push(x any)        { *p = append(*p, x.(Job)) }
func (p *pqueue) Pop() any {
	old := *p
	n := len(old)
	j := old[n-1]
	*p = old[:n-1]
	return j
}

// Queue is a thread-safe priority queue of Jobs.
type Queue struct {
	mu   sync.Mutex
	pq   pqueue
	next uint64
}

func New() *Queue { return &Queue{} }

func (q *Queue) Push(j Job) {
	q.mu.Lock()
	j.seq = q.next
	q.next++
	heap.Push(&q.pq, j)
	q.mu.Unlock()
}

func (q *Queue) pop() (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pq.Len() == 0 {
		return Job{}, false
	}
	return heap.Pop(&q.pq).(Job), true
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pq.Len()
}
```

- [x] **Step 4: Run test, expect pass** — `make test`.

- [x] **Step 5: Commit**
```bash
git add internal/queue
git commit -m "Add priority outbound job queue"
```

---

### Task 3: Rate-limited dispatcher with retry

**Files:**
- Create: `internal/queue/dispatcher.go`
- Test: `internal/queue/dispatcher_test.go`

**Interfaces:**
- Consumes: `Queue`, `Job` (Task 2), `golang.org/x/time/rate`.
- Produces:
  - `type RetryAfter struct { Seconds int }` implementing `error` (`Error() string`) — a job's `Run` returns this to signal a 429.
  - `type Dispatcher struct { ... }`, `func NewDispatcher(global *rate.Limiter, perChat func(chat int64) *rate.Limiter) *Dispatcher`.
  - `func (d *Dispatcher) Submit(chat int64, j Job)` — enqueues.
  - `func (d *Dispatcher) Run(ctx context.Context)` — pops jobs, waits on the global + per-chat limiter, runs; on `RetryAfter` re-enqueues the same job after sleeping `Seconds` (respecting ctx); on other errors logs and drops. Returns when ctx is cancelled and the queue drains, or immediately on cancel.
  - `func (d *Dispatcher) clock` seam: an internal `sleep func(context.Context, time.Duration)` field defaulting to a real sleep, overridable in tests.

- [x] **Step 1: Write the failing test**
```go
package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestDispatcherRunsAndRetriesOn429(t *testing.T) {
	d := NewDispatcher(rate.NewLimiter(rate.Inf, 1), func(int64) *rate.Limiter {
		return rate.NewLimiter(rate.Inf, 1)
	})
	// make sleep instant so the retry doesn't stall the test
	d.sleep = func(context.Context, time.Duration) {}

	var mu sync.Mutex
	attempts := 0
	done := make(chan struct{})
	d.Submit(-100123, Job{Priority: PrioHigh, Run: func(context.Context) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			return RetryAfter{Seconds: 1} // first attempt 429s
		}
		close(done)
		return nil
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not complete after retry")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2 (one 429 + one success)", attempts)
	}
}
```

- [x] **Step 2: Run test, expect failure** — add dependency + undefined.

Run: `./scripts/dev.sh get golang.org/x/time@v0.9.0 && make tidy`

- [x] **Step 3: Implement**
```go
package queue

import (
	"context"
	"fmt"
	"log"
	"time"

	"golang.org/x/time/rate"
)

// RetryAfter is returned by a Job.Run to signal Telegram 429 backoff.
type RetryAfter struct{ Seconds int }

func (r RetryAfter) Error() string { return fmt.Sprintf("retry after %ds", r.Seconds) }

// Dispatcher runs queued jobs under a global and per-chat rate limit.
type Dispatcher struct {
	q       *Queue
	global  *rate.Limiter
	perChat func(chat int64) *rate.Limiter
	jobs    map[uint64]int64 // seq -> chat, for per-chat limiter lookup
	sleep   func(context.Context, time.Duration)
	wake    chan struct{}
}

func NewDispatcher(global *rate.Limiter, perChat func(chat int64) *rate.Limiter) *Dispatcher {
	return &Dispatcher{
		q:       New(),
		global:  global,
		perChat: perChat,
		jobs:    map[uint64]int64{},
		sleep: func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		},
		wake: make(chan struct{}, 1),
	}
}

// Submit enqueues j for chat.
func (d *Dispatcher) Submit(chat int64, j Job) {
	d.q.mu.Lock()
	j.seq = d.q.next
	d.jobs[j.seq] = chat
	d.q.mu.Unlock()
	d.q.Push(j) // Push re-stamps seq; see note — we set chat by the stamped seq below
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run processes jobs until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		j, ok := d.q.pop()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
				continue
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		chat := d.jobs[j.seq]
		delete(d.jobs, j.seq)
		if err := d.global.Wait(ctx); err != nil {
			return
		}
		if lim := d.perChat(chat); lim != nil {
			if err := lim.Wait(ctx); err != nil {
				return
			}
		}
		if err := j.Run(ctx); err != nil {
			var ra RetryAfter
			if asRetry(err, &ra) {
				d.sleep(ctx, time.Duration(ra.Seconds)*time.Second)
				if ctx.Err() != nil {
					return
				}
				d.Submit(chat, j) // re-enqueue same job
				continue
			}
			log.Printf("queue job failed (chat %d): %v", chat, err)
		}
	}
}

func asRetry(err error, ra *RetryAfter) bool {
	if r, ok := err.(RetryAfter); ok {
		*ra = r
		return true
	}
	return false
}
```

> Note on `Submit`/`seq`: `Queue.Push` stamps `seq` from `q.next`. To keep the
> `seq→chat` map correct, Submit must map AFTER Push stamps. Implement Submit as:
> lock `q.mu`; `j.seq = q.next`; `d.jobs[j.seq] = chat`; `heap.Push(&q.pq, j)`;
> `q.next++`; unlock. (i.e. inline Push's body so stamping and mapping are atomic).
> Replace the Step-3 `Submit` body with that atomic version; the `container/heap`
> import moves into dispatcher.go, or expose a `q.pushLocked(j)` helper in queue.go.
> Keep the public `Queue.Push` for Task 2's test.

- [x] **Step 3b: Make Submit atomic** — in `internal/queue/queue.go` add:
```go
// pushMapped stamps seq, records chat for seq, and pushes — all under the lock.
func (q *Queue) pushMapped(j Job, chat int64, jobs map[uint64]int64) {
	q.mu.Lock()
	j.seq = q.next
	q.next++
	jobs[j.seq] = chat
	heap.Push(&q.pq, j)
	q.mu.Unlock()
}
```
and replace `Dispatcher.Submit` body with:
```go
func (d *Dispatcher) Submit(chat int64, j Job) {
	d.q.pushMapped(j, chat, d.jobs)
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
```
> The `d.jobs` map is now touched under `q.mu` in `pushMapped` and under no lock in
> `Run`. Run pops under `q.mu` (inside `pop`) but reads `d.jobs` after. Guard `d.jobs`
> with its own mutex OR read+delete it inside a `q.mu`-locked helper. Use a dedicated
> `d.jmu sync.Mutex` around every `d.jobs` access (in `pushMapped` pass a locker, or
> simpler: give Dispatcher its own `jmu` and lock it in Submit and in Run when reading).
> Choose the dedicated-mutex approach: add `jmu sync.Mutex` to Dispatcher; in Submit
> lock `jmu` around the `jobs[seq]=chat` write (do the seq stamping via a `q` helper that
> returns the seq); in Run lock `jmu` around the read+delete. Ensure `go test -race` is clean.

- [x] **Step 4: Run test with race, expect pass**

Run: `./scripts/dev.sh test -race ./internal/queue/`
Expected: PASS, no race warnings. (If a race appears on `d.jobs`, tighten the `jmu` locking as noted.)

- [x] **Step 5: Commit**
```bash
git add internal/queue go.mod go.sum
git commit -m "Add rate-limited dispatcher with retry"
```

---

### Task 4: Real Telegram port (library-backed), routed through the dispatcher

**Files:**
- Create: `internal/telegram/livept.go`
- Test: `internal/telegram/livept_test.go`

**Interfaces:**
- Consumes: `github.com/go-telegram/bot`, `Port`/`Perms`/`Member`/`AdminMessage`/`Button` (Tasks 1), `queue.Dispatcher`/`queue.Job`/`queue.RetryAfter`/`queue.Priority` (Tasks 2–3).
- Produces: `type LivePort struct { ... }`, `func NewLivePort(b *bot.Bot, disp *queue.Dispatcher, prio func(method string) queue.Priority) *LivePort` implementing `Port`. Each method builds the library params and submits a `queue.Job` whose `Run` calls the library and maps a Telegram 429 to `queue.RetryAfter`. Synchronous-result methods (`CopyMessages` returns ids; `GetChatAdministrators` returns members) run inline through the dispatcher via a result channel. `DeleteMessages` splits ids into batches of ≤100.
- Also produces `func mapRetry(err error) error` — detects the library's 429 error and returns `queue.RetryAfter{Seconds: n}`, else the original error.

- [x] **Step 1: Write the failing test** (unit-tests the pure helpers, not the network):
```go
package telegram

import (
	"testing"
)

func TestBatchIDs(t *testing.T) {
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	batches := batchIDs(ids, 100)
	if len(batches) != 3 || len(batches[0]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("batches: %d sizes %d/%d/%d", len(batches), len(batches[0]), len(batches[1]), len(batches[2]))
	}
}
```

- [x] **Step 2: Run test, expect failure** — undefined `batchIDs`.

- [x] **Step 3: Implement `livept.go`**. Provide `batchIDs`, `mapRetry`, and `LivePort` implementing every `Port` method by submitting jobs. For methods that return values, use a buffered result channel the job closes over. Consult `./scripts/dev.sh doc github.com/go-telegram/bot` for exact method/param names (`CopyMessages`, `DeleteMessages`, `BanChatMember`, `RestrictChatMember`, `SendMessage`, `GetChatAdministrators`, `AnswerCallbackQuery`, `EditMessageReplyMarkup`, `BanChatSenderChat`). Minimum content for the helper under test:
```go
package telegram

// batchIDs splits ids into chunks of at most size (Telegram deleteMessages caps at 100).
func batchIDs(ids []int, size int) [][]int {
	var out [][]int
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}
```
Then implement `LivePort` and `mapRetry` (full method bodies calling the library through `disp.Submit`, priority chosen via the `prio` func by method name). Each destructive method uses `PrioHigh`; `SendAdmin`/`EditAdminMarkup`/`AnswerCallback` use `PrioNormal`.

> The result-returning pattern (for CopyMessages/GetChatAdministrators):
> ```go
> func (p *LivePort) CopyMessages(ctx context.Context, dst, src int64, ids []int) ([]int, error) {
> 	type res struct{ ids []int; err error }
> 	ch := make(chan res, 1)
> 	p.disp.Submit(dst, queue.Job{Priority: queue.PrioNormal, Run: func(ctx context.Context) error {
> 		out, err := p.callCopyMessages(ctx, dst, src, ids) // library call + mapRetry
> 		if ra, ok := err.(queue.RetryAfter); ok { return ra } // let dispatcher retry
> 		ch <- res{out, err}
> 		return nil
> 	}})
> 	select {
> 	case <-ctx.Done(): return nil, ctx.Err()
> 	case r := <-ch: return r.ids, r.err
> 	}
> }
> ```
> On retry the job re-runs `callCopyMessages`; only a terminal outcome writes `ch`.

- [x] **Step 4: Run test + build, expect pass**

Run: `make test && make build`
Expected: `batchIDs` test passes; `LivePort` compiles and satisfies `Port` (add `var _ Port = (*LivePort)(nil)` in livept.go).

- [x] **Step 5: Commit**
```bash
git add internal/telegram
git commit -m "Add library-backed live telegram port"
```

---

### Task 5: Album buffer

**Files:**
- Create: `internal/telegram/album.go`
- Test: `internal/telegram/album_test.go`

**Interfaces:**
- Consumes: `domain.Message`.
- Produces:
  - `type AlbumBuffer struct { ... }`, `func NewAlbumBuffer(window time.Duration, flush func(parts []domain.Message)) *AlbumBuffer`.
  - `func (a *AlbumBuffer) Add(m domain.Message) bool` — returns true if the message is a standalone (no `MediaGroupID`) that the caller should handle immediately; false if it was buffered as an album part.
  - Album parts sharing `(ChatID, MediaGroupID)` are collected; `window` after the first part arrives, `flush(parts)` is called once with all parts in arrival order.
  - `func (a *AlbumBuffer) clock` seam: `now func() time.Time` and `afterFunc func(time.Duration, func()) *time.Timer` fields, overridable in tests (default to `time.Now`/`time.AfterFunc`).
  - `func (a *AlbumBuffer) Stop()` — stops pending timers (shutdown).

- [x] **Step 1: Write the failing test** (fake clock, deterministic):
```go
package telegram

import (
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestAlbumBufferGroupsParts(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]domain.Message
	var fire func()
	a := NewAlbumBuffer(700*time.Millisecond, func(parts []domain.Message) {
		mu.Lock()
		cp := append([]domain.Message(nil), parts...)
		flushed = append(flushed, cp)
		mu.Unlock()
	})
	a.afterFunc = func(_ time.Duration, fn func()) *time.Timer { fire = fn; return time.NewTimer(time.Hour) }

	standalone := a.Add(domain.Message{ChatID: -1, MessageID: 1})
	if !standalone {
		t.Fatal("message without media_group_id must be standalone")
	}
	if a.Add(domain.Message{ChatID: -1, MessageID: 10, MediaGroupID: "g"}) {
		t.Fatal("first album part must be buffered, not standalone")
	}
	if a.Add(domain.Message{ChatID: -1, MessageID: 11, MediaGroupID: "g"}) {
		t.Fatal("second album part must be buffered")
	}
	fire() // window elapses
	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 || len(flushed[0]) != 2 {
		t.Fatalf("expected one flush of 2 parts, got %v", flushed)
	}
	if flushed[0][0].MessageID != 10 || flushed[0][1].MessageID != 11 {
		t.Fatalf("parts out of order: %v", flushed[0])
	}
}
```

- [x] **Step 2: Run test, expect failure** — undefined.

- [x] **Step 3: Implement `album.go`**:
```go
package telegram

import (
	"sync"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

type albumKey struct {
	chat  int64
	group string
}

// AlbumBuffer coalesces album parts sharing (chat_id, media_group_id) that
// Telegram delivers as separate updates, flushing them together after a window.
type AlbumBuffer struct {
	mu        sync.Mutex
	window    time.Duration
	flush     func(parts []domain.Message)
	pending   map[albumKey][]domain.Message
	timers    map[albumKey]*time.Timer
	now       func() time.Time
	afterFunc func(time.Duration, func()) *time.Timer
}

func NewAlbumBuffer(window time.Duration, flush func(parts []domain.Message)) *AlbumBuffer {
	return &AlbumBuffer{
		window:    window,
		flush:     flush,
		pending:   map[albumKey][]domain.Message{},
		timers:    map[albumKey]*time.Timer{},
		now:       time.Now,
		afterFunc: time.AfterFunc,
	}
}

// Add buffers album parts and returns false for them; returns true for a
// standalone message the caller should handle immediately.
func (a *AlbumBuffer) Add(m domain.Message) bool {
	if m.MediaGroupID == "" {
		return true
	}
	k := albumKey{m.ChatID, m.MediaGroupID}
	a.mu.Lock()
	first := len(a.pending[k]) == 0
	a.pending[k] = append(a.pending[k], m)
	if first {
		a.timers[k] = a.afterFunc(a.window, func() { a.fire(k) })
	}
	a.mu.Unlock()
	return false
}

func (a *AlbumBuffer) fire(k albumKey) {
	a.mu.Lock()
	parts := a.pending[k]
	delete(a.pending, k)
	delete(a.timers, k)
	a.mu.Unlock()
	if len(parts) > 0 {
		a.flush(parts)
	}
}

// Stop cancels all pending timers (call at shutdown).
func (a *AlbumBuffer) Stop() {
	a.mu.Lock()
	for _, t := range a.timers {
		t.Stop()
	}
	a.mu.Unlock()
}
```

- [x] **Step 4: Run test with race, expect pass**

Run: `./scripts/dev.sh test -race ./internal/telegram/`

- [x] **Step 5: Commit**
```bash
git add internal/telegram
git commit -m "Add album buffer for media groups"
```

---

### Task 6: Incident machine — real evidence ids, reprocess guard, hard-deny admin notice

**Files:**
- Modify: `internal/incident/machine.go`
- Test: `internal/incident/machine_test.go`

**Interfaces:**
- Consumes: existing `Machine`, `Repo`, `telegram.Port`/`AdminMessage`.
- Produces: three behavior changes, each covered by a new test:
  1. `Handle` uses `InsertPending`'s `fresh` return: if `fresh == false` (incident already exists), it returns `nil` immediately (reprocess guard) — no second evidence copy or action.
  2. `AdminMessage.CopyMessageIDs` is set to the admin-chat copy ids (`adminIDs` returned by `CopyMessages`), not the source ids.
  3. On the hard-deny evidence-failure path (confidence ≥ 0.9, copy failed) the machine still sends an admin summary (text-only fallback via `SendAdmin` with empty `CopyMessageIDs`) so admins are notified of the action.

- [x] **Step 1: Write the failing tests** — add to `machine_test.go`:
```go
func TestReprocessGuardSkipsDuplicate(t *testing.T) {
	f := fake.New()
	repo := &stubRepo{fresh: false} // incident already exists
	m := New(f, repo, 999)
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "CopyMessages" || c == "BanMember" {
			t.Fatalf("duplicate incident must be skipped; calls=%v", f.Calls())
		}
	}
}

func TestHardDenyNotifiesAdminOnEvidenceFailure(t *testing.T) {
	f := fake.New()
	f.CopyErr = errors.New("copy failed")
	m := New(f, &stubRepo{fresh: true}, 999)
	inc := liveIncident(false)
	inc.Verdict.Confidence = 0.99
	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	var notified bool
	for _, c := range f.Calls() {
		if c == "SendAdmin" {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("hard-deny with failed evidence must still notify admin; calls=%v", f.Calls())
	}
}
```
Update `stubRepo` to carry a `fresh bool` and return it from `InsertPending`:
```go
type stubRepo struct {
	state domain.IncidentState
	fresh bool
}
func (r *stubRepo) InsertPending(int64, int, int64, bool) (int64, bool, error) { return 1, r.fresh, nil }
```
and set `fresh: true` in the existing tests' `stubRepo` literals so they still exercise the full path.

- [x] **Step 2: Run tests, expect the two new ones to fail.**

- [x] **Step 3: Implement** — in `machine.go`:
  - Capture `fresh` from `InsertPending`; if `!fresh`, `return nil` right after insert.
  - In the success branch, pass `CopyMessageIDs: adminIDs` to `SendAdmin`.
  - In the hard-deny `copyErr` branch, after setting `StateEvidenceFailed`, call `SendAdmin` with `CopyMessageIDs: nil` and a `Text` noting evidence copy failed, before proceeding to `applyAction`.

- [x] **Step 4: Run tests, expect pass** (all incident tests green).

- [x] **Step 5: Commit**
```bash
git add internal/incident
git commit -m "Harden incident machine evidence handling"
```

---

### Task 7: Admin callback handler with RBAC

**Files:**
- Create: `internal/admin/callbacks.go`
- Test: `internal/admin/callbacks_test.go`

**Interfaces:**
- Consumes: `telegram.Port`/`Member`/`Button`, `store.*DB`, `domain`.
- Produces:
  - `type Action string` with `ActFalsePositive="fp"`, `ActLiftNoLearn="lift"`, `ActConfirmSpam="confirm"`, `ActDeleteEvidence="delevi"`.
  - `func Buttons(incidentKey string) [][]telegram.Button` — the 4-button layout, each `Data` = `"<act>:<incidentKey>"` (≤64 bytes; incidentKey is the opaque incident id).
  - `func ParseCallback(data string) (Action, string, bool)` — split `act:key`.
  - `type Handler struct { ... }`, `func NewHandler(port telegram.Port, db *store.DB, operators map[int64]bool) *Handler`.
  - `func (h *Handler) Authorized(ctx, sourceChatID, presserID int64) (bool, error)` — true if presser is a global operator OR an admin of the source chat (via `port.GetChatAdministrators`).
  - `func (h *Handler) Handle(ctx, cb Callback) error` where `Callback{ID string; Data string; PresserID int64}` — parse, look up the incident's source chat from the store, RBAC-check, act (map each Action to unban/unrestrict + sample, etc. — for M2 the store-side sample writes are stubs writing to `samples` with origin "user"), then `AnswerCallback`.

- [x] **Step 1: Write the failing test** — RBAC is the load-bearing bit:
```go
package admin

import (
	"context"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

func TestAuthorizedOperatorAndChatAdmin(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 50}}
	h := NewHandler(f, nil, map[int64]bool{7: true})

	ok, _ := h.Authorized(context.Background(), -100123, 7) // global operator
	if !ok {
		t.Fatal("global operator must be authorized")
	}
	ok, _ = h.Authorized(context.Background(), -100123, 50) // source-chat admin
	if !ok {
		t.Fatal("source-chat admin must be authorized")
	}
	ok, _ = h.Authorized(context.Background(), -100123, 99) // neither
	if ok {
		t.Fatal("non-admin non-operator must be rejected")
	}
}

func TestParseCallbackRoundTrip(t *testing.T) {
	btns := Buttons("abc123")
	// first button data must parse back
	act, key, ok := ParseCallback(btns[0][0].Data)
	if !ok || key != "abc123" || act == "" {
		t.Fatalf("parse: act=%q key=%q ok=%v", act, key, ok)
	}
}
```

- [x] **Step 2: Run test, expect failure** — undefined.

- [x] **Step 3: Implement `callbacks.go`** — `Buttons`, `ParseCallback`, `Handler`, `Authorized` (operator check first, then `GetChatAdministrators` membership), and `Handle` (RBAC then act then AnswerCallback; the per-action store effects can be minimal writes for M2, but `Authorized` must gate every branch). Keep each callback's `Data` under 64 bytes.

- [x] **Step 4: Run test, expect pass** — `make test`.

- [x] **Step 5: Commit**
```bash
git add internal/admin
git commit -m "Add admin callback handler with RBAC"
```

---

### Task 8: Wire the machine, album buffer, and dispatcher into the handler and main

**Files:**
- Modify: `internal/telegram/bot.go`
- Modify: `cmd/tg-antispam/main.go`
- Test: `internal/telegram/bot_wire_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `Handler.OnMessage` now: dedup → skip unregistered/immune → album-buffer (standalone handled now; album parts flushed together) → build a `domain.Incident` and call the wired `IncidentMachine.Handle`. In M2 there is still no detector, so `OnMessage` builds an incident ONLY when a package-level test hook / config "flag everything in dry-run" switch says to; by default it registers the chat and logs (M1 behavior) unless `cfg` marks the chat live AND a verdict source is present. To keep M2 testable without detectors, add `Handler.decide func(domain.Message) (domain.Verdict, bool)` defaulting to "no verdict" (returns false), which M3 replaces with the cascade. Tests inject a decide hook that returns an actionable verdict and assert `machine.Handle` was invoked with evidence-first ordering (using the fake port).
  - `OnEditedMessage(ctx, updateID int64, m domain.Message)` — same pipeline for edits (routes `edited_message`, an M1-deferred item).
  - `main()` constructs the `LivePort` (dispatcher + limiters), the `AlbumBuffer`, the real `incident.Machine` bound to the LivePort, the admin callback handler, and registers a callback handler on the bot; starts `dispatcher.Run` in a goroutine tied to ctx. The default update handler routes `update.Message`, `update.EditedMessage`, and `update.CallbackQuery`.

- [x] **Step 1: Write the failing test** — inject a decide hook and assert machine invocation with evidence-first ordering:
```go
package telegram

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

func TestOnMessageDrivesMachineWhenVerdictActionable(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	f.SendAdminID = 5
	m := incident.New(f, db, 999)
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto"}})
	seq := NewSequencer()
	h := NewHandler(db, seq, cfg, m)
	h.decide = func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
	}
	h.OnMessage(context.Background(), 1, domain.Message{ChatID: -100123, MessageID: 55, Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser}})
	seq.Wait()

	calls := f.Calls()
	var iCopy, iBan = -1, -1
	for i, c := range calls {
		if c == "CopyMessages" && iCopy < 0 {
			iCopy = i
		}
		if c == "BanMember" && iBan < 0 {
			iBan = i
		}
	}
	if iCopy < 0 || iBan < 0 || iCopy > iBan {
		t.Fatalf("expected evidence copy before ban; calls=%v", calls)
	}
}
```

- [x] **Step 2: Run test, expect failure** — `decide` field/pipeline not present.

- [x] **Step 3: Implement** — add `decide` hook (default returns `(Verdict{}, false)`), the album buffer wiring, incident construction, `OnMessage`/`OnEditedMessage`, and the `main()` construction of LivePort/dispatcher/machine/admin handler + callback routing. Non-actionable/no-verdict path keeps M1 behavior (register + log). When `decide` returns actionable, build `domain.Incident{ChatID, MessageIDs:[messageID or album ids], Sender, Verdict, DryRun: chat dry-run}` and `machine.Handle` inside the sequencer job.

- [x] **Step 4: Run test + build + vet, expect pass**

Run: `make test && make build && make vet`

- [x] **Step 5: Commit**
```bash
git add internal/telegram cmd
git commit -m "Wire machine album buffer and dispatcher"
```

---

## Milestone 2 Definition of Done

- `make test` (and `-race` on `queue`, `telegram`) green; `make build`/`make vet` clean.
- Every Telegram side effect flows through the priority + rate-limited dispatcher; deletes batch at 100; 429 triggers retry.
- Albums are buffered and copied as a unit before any action.
- The incident machine is invoked from `OnMessage`/`OnEditedMessage`, still evidence-first, and skips duplicates.
- The admin four-button layout exists with per-callback RBAC (operator or source-chat admin).
- Nothing installed on host; all in `golang:1.26`.

**Next milestone (separate plan):** M3 — the normalizer + trust-gate + hard rules + behavioral checks, replacing the `decide` hook with the real detection cascade, still defaulting new chats to dry-run.
