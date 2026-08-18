# telegram-antispam M1 — Foundation & Delivery Spine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the correctness spine of the bot — config, storage, the incident state machine, and the polling/sequencer delivery path — so a process can receive Telegram updates for registered chats, persist them idempotently, and drive an incident through evidence→action→cleanup (dry-run) with everything testable off-network.

**Architecture:** Detection stays out of this milestone. We build the layers the spec says matter most: delivery, ordering, reversibility. Library types are confined to `internal/telegram`; everything else works on `internal/domain` types and is tested against a fake Telegram port. Storage is SQLite (WAL, single writer goroutine).

**Tech Stack:** Go 1.24+, `github.com/go-telegram/bot`, `modernc.org/sqlite` (cgo-free), `github.com/fsnotify/fsnotify` (config reload), `gopkg.in/yaml.v3`. All build/test runs in Docker (`golang:1.26`) — nothing is installed on the host.

**Spec:** `docs/superpowers/specs/2026-08-18-telegram-antispam-design.md` (this plan implements build-order items 1–3 of spec §15).

## Global Constraints

- **Module path:** `github.com/stufently/telegram-antispam` (copy verbatim in every import).
- **No host installs.** Every `go` command runs inside `golang:1.26` via `scripts/dev.sh` (Task 1). Never run `go` on the host.
- **Docker as user `deploy` (1002:1002).** `scripts/dev.sh` passes `--user "$(id -u):$(id -g)"` so files stay owned by the caller. Go caches live under `.gopath/` in the repo (gitignored), never a root-owned named volume.
- **SQLite pragmas (spec §9):** every connection sets `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL` via the DSN. Writes go through a single writer goroutine.
- **Message identity is `(chat_id, message_id)`, never a text hash** (spec §9).
- **`allowed_updates` (spec §2):** `message`, `edited_message`, `callback_query`, `chat_member`, `my_chat_member`, `message_reaction`.
- **English only** in code, comments, identifiers, commit messages.
- **Commit style (user rule):** ≤50-char imperative subject, no Co-Authored-By, no signatures, a new commit per task.
- **TDD:** every task writes the failing test first, watches it fail, writes minimal code, watches it pass, commits.

---

### Task 1: Project scaffold and Docker dev harness

**Files:**
- Create: `go.mod`
- Create: `scripts/dev.sh`
- Create: `Makefile`
- Modify: `.gitignore`
- Create: `internal/version/version.go`
- Test: `internal/version/version_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `version.String() string`; the `scripts/dev.sh` runner (`./scripts/dev.sh <go-args...>`) and `make test` / `make vet` / `make build`, used by every later task's test steps.

- [ ] **Step 1: Create the module file**

`go.mod`:
```
module github.com/stufently/telegram-antispam

go 1.24
```

- [ ] **Step 2: Create the Docker Go runner**

`scripts/dev.sh` (make executable with `chmod +x`):
```bash
#!/usr/bin/env bash
# Runs `go` inside golang:1.26 as the calling user, with caches under the
# repo so produced files stay owned by deploy. No host Go is used.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .gopath/cache
exec docker run --rm --network host \
  -e HTTP_PROXY -e HTTPS_PROXY -e NO_PROXY \
  -e GOSUMDB=off -e GOFLAGS=-mod=mod \
  -e GOPATH=/src/.gopath -e GOCACHE=/src/.gopath/cache \
  -v "$PWD":/src -w /src \
  --user "$(id -u):$(id -g)" \
  golang:1.26 go "$@"
```

> Note: `GOSUMDB=off` because `sum.golang.org` is unreachable through this
> network's proxy. Integrity is still pinned by the committed `go.sum`; the
> checksum database is only consulted when adding new modules.

- [ ] **Step 3: Create the Makefile**

`Makefile`:
```makefile
.PHONY: test vet build tidy
test:
	./scripts/dev.sh test ./...
vet:
	./scripts/dev.sh vet ./...
build:
	./scripts/dev.sh build ./...
tidy:
	./scripts/dev.sh mod tidy
```

- [ ] **Step 4: Extend .gitignore**

Append to `.gitignore`:
```
/.gopath/
/tg-antispam
```

- [ ] **Step 5: Write the failing test**

`internal/version/version_test.go`:
```go
package version

import "testing"

func TestStringNotEmpty(t *testing.T) {
	if String() == "" {
		t.Fatal("version.String() must not be empty")
	}
}
```

- [ ] **Step 6: Run the test, expect failure**

Run: `chmod +x scripts/dev.sh && make test`
Expected: FAIL — `undefined: String`.

- [ ] **Step 7: Implement**

`internal/version/version.go`:
```go
// Package version reports the build version of the bot.
package version

// version is overridden at build time via -ldflags; dev default below.
var version = "dev"

// String returns the current build version.
func String() string { return version }
```

- [ ] **Step 8: Run the test, expect pass**

Run: `make test`
Expected: PASS (`ok  .../internal/version`).

- [ ] **Step 9: Commit**

```bash
git add go.mod scripts Makefile .gitignore internal/version
git commit -m "Add module scaffold and docker dev harness"
```

---

### Task 2: Domain types and enums

**Files:**
- Create: `internal/domain/enums.go`
- Create: `internal/domain/types.go`
- Test: `internal/domain/types_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - Enums (string types): `SenderKind` (`SenderUser`, `SenderExternalChannel`, `SenderAnonAdmin`, `SenderLinkedChannel`, `SenderBot`); `Action` (`ActionNone`, `ActionDeleteMute`, `ActionMute`, `ActionBan`, `ActionDeleteOnly`, `ActionQuarantine`); `Scope` (`ScopeGlobal`, `ScopeChat`); `IncidentState` (`StatePending`, `StateEvidenced`, `StateActed`, `StateCleaned`, `StateDone`, `StateEvidenceFailed`).
  - `type Sender struct { Kind SenderKind; UserID int64; SenderChatID int64; Username, DisplayName string }`
  - `type Message struct { ChatID int64; MessageID int; ThreadID int; MediaGroupID string; Sender Sender; Text string; Date int64; IsAutomaticForward bool; LinkedChatID int64 }`
  - `type Signal struct { Name string; Detail string }`
  - `type Verdict struct { Action Action; Scope Scope; Confidence float64; Signals []Signal; Reason string }`
  - `type Incident struct { ChatID int64; MessageIDs []int; ThreadID int; Sender Sender; Verdict Verdict; State IncidentState; DryRun bool; AdminMessageIDs []int }`
  - Method `func (v Verdict) IsActionable() bool` (true when `Action != ActionNone`).

- [ ] **Step 1: Write the failing test**

`internal/domain/types_test.go`:
```go
package domain

import "testing"

func TestVerdictIsActionable(t *testing.T) {
	if (Verdict{Action: ActionNone}).IsActionable() {
		t.Error("ActionNone must not be actionable")
	}
	if !(Verdict{Action: ActionDeleteMute}).IsActionable() {
		t.Error("ActionDeleteMute must be actionable")
	}
}

func TestIncidentKeyFields(t *testing.T) {
	inc := Incident{ChatID: -100123, MessageIDs: []int{5, 6}, State: StatePending}
	if inc.ChatID != -100123 || len(inc.MessageIDs) != 2 || inc.State != StatePending {
		t.Fatal("incident fields not wired")
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Implement enums**

`internal/domain/enums.go`:
```go
// Package domain holds pure types shared across the bot. It imports no
// project packages and no Telegram library types.
package domain

type SenderKind string

const (
	SenderUser            SenderKind = "user"
	SenderExternalChannel SenderKind = "external_channel"
	SenderAnonAdmin       SenderKind = "anon_admin"
	SenderLinkedChannel   SenderKind = "linked_channel"
	SenderBot             SenderKind = "bot"
)

type Action string

const (
	ActionNone       Action = "none"
	ActionDeleteMute Action = "delete_mute"
	ActionMute       Action = "mute"
	ActionBan        Action = "ban"
	ActionDeleteOnly Action = "delete_only"
	ActionQuarantine Action = "quarantine"
)

type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeChat   Scope = "chat"
)

type IncidentState string

const (
	StatePending        IncidentState = "pending"
	StateEvidenced      IncidentState = "evidenced"
	StateActed          IncidentState = "acted"
	StateCleaned        IncidentState = "cleaned"
	StateDone           IncidentState = "done"
	StateEvidenceFailed IncidentState = "evidence_failed"
)
```

- [ ] **Step 4: Implement structs**

`internal/domain/types.go`:
```go
package domain

// Sender identifies who sent a message, already classified (see spec §4).
type Sender struct {
	Kind         SenderKind
	UserID       int64
	SenderChatID int64
	Username     string
	DisplayName  string
}

// Message is the normalized-envelope of an incoming Telegram message. Text
// normalization for detection happens later; this is the delivery-layer view.
type Message struct {
	ChatID             int64
	MessageID          int
	ThreadID           int
	MediaGroupID       string
	Sender             Sender
	Text               string
	Date               int64
	IsAutomaticForward bool
	LinkedChatID       int64
}

// Signal is one explainable reason produced by a detector.
type Signal struct {
	Name   string
	Detail string
}

// Verdict is the detection outcome.
type Verdict struct {
	Action     Action
	Scope      Scope
	Confidence float64
	Signals    []Signal
	Reason     string
}

// IsActionable reports whether the verdict requires side effects.
func (v Verdict) IsActionable() bool { return v.Action != ActionNone }

// Incident is a persisted unit of work keyed by (ChatID, MessageIDs).
type Incident struct {
	ChatID          int64
	MessageIDs      []int
	ThreadID        int
	Sender          Sender
	Verdict         Verdict
	State           IncidentState
	DryRun          bool
	AdminMessageIDs []int
}
```

- [ ] **Step 5: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain
git commit -m "Add domain types and enums"
```

---

### Task 3: Telegram port interface and fake

**Files:**
- Create: `internal/telegram/port.go`
- Create: `internal/telegram/fake/fake.go`
- Test: `internal/telegram/fake/fake_test.go`

**Interfaces:**
- Consumes: `internal/domain`.
- Produces:
  - `type Perms struct { CanSend bool }` and `type Member struct { UserID int64; Status string; Username, DisplayName string }` in package `telegram`.
  - `type AdminMessage struct { Text string; IncidentKey string; SourceChatID int64; CopiedFromChatID int64; CopyMessageIDs []int }`.
  - `type Port interface` with methods:
    - `CopyMessages(ctx context.Context, dstChat, srcChat int64, ids []int) ([]int, error)`
    - `DeleteMessages(ctx context.Context, chat int64, ids []int) error`
    - `BanMember(ctx context.Context, chat, user int64) error`
    - `RestrictMember(ctx context.Context, chat, user int64, perms Perms, until int64) error`
    - `SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error)`
  - `fake.New() *Fake` with an ordered call log: `Fake.Calls() []string`, plus `Fake.CopyErr`, `Fake.SendAdminID` knobs. `*Fake` implements `telegram.Port`.

- [ ] **Step 1: Write the failing test**

`internal/telegram/fake/fake_test.go`:
```go
package fake

import (
	"context"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

func TestFakeImplementsPortAndLogsOrder(t *testing.T) {
	var _ telegram.Port = New()

	f := New()
	f.SendAdminID = 42
	ctx := context.Background()

	ids, err := f.CopyMessages(ctx, 999, -100123, []int{5})
	if err != nil || len(ids) != 1 {
		t.Fatalf("copy: ids=%v err=%v", ids, err)
	}
	id, err := f.SendAdmin(ctx, 999, telegram.AdminMessage{IncidentKey: "k"})
	if err != nil || id != 42 {
		t.Fatalf("sendadmin: id=%d err=%v", id, err)
	}
	if err := f.BanMember(ctx, -100123, 7); err != nil {
		t.Fatal(err)
	}

	got := f.Calls()
	want := []string{"CopyMessages", "SendAdmin", "BanMember"}
	if len(got) != len(want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %q want %q", i, got[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — package `telegram` and `fake` do not exist.

- [ ] **Step 3: Implement the port**

`internal/telegram/port.go`:
```go
// Package telegram is the only package that talks to the Telegram library.
// The rest of the bot depends on Port so it can be tested with a fake.
package telegram

import "context"

// Perms is the subset of chat permissions the bot toggles when muting.
type Perms struct {
	CanSend bool
}

// Member is a chat member as the bot needs it.
type Member struct {
	UserID      int64
	Status      string
	Username    string
	DisplayName string
}

// AdminMessage is a summary sent to the admin chat alongside copied evidence.
type AdminMessage struct {
	Text             string
	IncidentKey      string
	SourceChatID     int64
	CopiedFromChatID int64
	CopyMessageIDs   []int
}

// Port is the narrow Telegram surface the incident logic depends on.
type Port interface {
	CopyMessages(ctx context.Context, dstChat, srcChat int64, ids []int) ([]int, error)
	DeleteMessages(ctx context.Context, chat int64, ids []int) error
	BanMember(ctx context.Context, chat, user int64) error
	RestrictMember(ctx context.Context, chat, user int64, perms Perms, until int64) error
	SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error)
}
```

- [ ] **Step 4: Implement the fake**

`internal/telegram/fake/fake.go`:
```go
// Package fake is an in-memory telegram.Port for tests. It records the order
// of calls so tests can assert evidence-before-action ordering.
package fake

import (
	"context"
	"sync"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

type Fake struct {
	mu    sync.Mutex
	calls []string

	// knobs
	CopyErr     error
	SendAdminID int
}

func New() *Fake { return &Fake{} }

func (f *Fake) log(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

// Calls returns the recorded call names in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *Fake) CopyMessages(_ context.Context, _, _ int64, ids []int) ([]int, error) {
	f.log("CopyMessages")
	if f.CopyErr != nil {
		return nil, f.CopyErr
	}
	out := make([]int, len(ids))
	for i := range ids {
		out[i] = 100000 + ids[i]
	}
	return out, nil
}

func (f *Fake) DeleteMessages(_ context.Context, _ int64, _ []int) error {
	f.log("DeleteMessages")
	return nil
}

func (f *Fake) BanMember(_ context.Context, _, _ int64) error {
	f.log("BanMember")
	return nil
}

func (f *Fake) RestrictMember(_ context.Context, _, _ int64, _ telegram.Perms, _ int64) error {
	f.log("RestrictMember")
	return nil
}

func (f *Fake) SendAdmin(_ context.Context, _ int64, _ telegram.AdminMessage) (int, error) {
	f.log("SendAdmin")
	return f.SendAdminID, nil
}
```

- [ ] **Step 5: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/telegram
git commit -m "Add telegram port interface and fake"
```

---

### Task 4: Sender identity classification

**Files:**
- Create: `internal/detect/identity.go`
- Test: `internal/detect/identity_test.go`

**Interfaces:**
- Consumes: `internal/domain`.
- Produces: `func ClassifySender(in ClassifyInput) domain.SenderKind` and `type ClassifyInput struct { FromID int64; IsBot bool; SenderChatID int64; SenderChatType string; ChatID int64; LinkedChatID int64; IsAutomaticForward bool }`. Constant `AnonAdminBotID int64 = 1087968824`, `ChannelBotID int64 = 136817688`, `ServiceNotificationsID int64 = 777000`.

- [ ] **Step 1: Write the failing test**

`internal/detect/identity_test.go`:
```go
package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestClassifySender(t *testing.T) {
	cases := []struct {
		name string
		in   ClassifyInput
		want domain.SenderKind
	}{
		{"plain user", ClassifyInput{FromID: 7, ChatID: -100123}, domain.SenderUser},
		{"bot", ClassifyInput{FromID: 9, IsBot: true, ChatID: -100123}, domain.SenderBot},
		{"anon admin", ClassifyInput{FromID: AnonAdminBotID, SenderChatID: -100123, SenderChatType: "supergroup", ChatID: -100123}, domain.SenderAnonAdmin},
		{"linked channel autoforward", ClassifyInput{FromID: ServiceNotificationsID, SenderChatID: -100777, SenderChatType: "channel", ChatID: -100123, LinkedChatID: -100777, IsAutomaticForward: true}, domain.SenderLinkedChannel},
		{"external channel", ClassifyInput{SenderChatID: -100888, SenderChatType: "channel", ChatID: -100123, LinkedChatID: -100777}, domain.SenderExternalChannel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifySender(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `ClassifySender`, `ClassifyInput`, constants.

- [ ] **Step 3: Implement**

`internal/detect/identity.go`:
```go
// Package detect holds pure detection logic: no side effects, no Telegram
// library types. This file classifies the four sender identities (spec §4).
package detect

import "github.com/stufently/telegram-antispam/internal/domain"

const (
	AnonAdminBotID         int64 = 1087968824
	ChannelBotID           int64 = 136817688
	ServiceNotificationsID int64 = 777000
)

// ClassifyInput is the raw sender data the adapter extracts from an update.
type ClassifyInput struct {
	FromID             int64
	IsBot              bool
	SenderChatID       int64
	SenderChatType     string
	ChatID             int64
	LinkedChatID       int64
	IsAutomaticForward bool
}

// ClassifySender maps raw sender data to a SenderKind. Order matters: the
// anonymous-admin and linked-channel checks must precede the generic
// external-channel case.
func ClassifySender(in ClassifyInput) domain.SenderKind {
	if in.SenderChatID != 0 {
		switch {
		case in.SenderChatID == in.ChatID || in.FromID == AnonAdminBotID:
			return domain.SenderAnonAdmin
		case in.IsAutomaticForward && in.SenderChatID == in.LinkedChatID:
			return domain.SenderLinkedChannel
		default:
			return domain.SenderExternalChannel
		}
	}
	if in.IsBot {
		return domain.SenderBot
	}
	return domain.SenderUser
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/detect
git commit -m "Add sender identity classification"
```

---

### Task 5: Config schema, load, and validation

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/testdata/valid.yaml`
- Create: `internal/config/testdata/bad_action.yaml`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `internal/domain`.
- Produces:
  - `type Config struct { BotToken string; AdminChatID int64; Chats ChatsPolicy; Action domain.Action }` with `type ChatsPolicy struct { Mode string; StartInDryRun bool; Allowlist []int64 }`.
  - `func Load(path string) (*Config, error)` — read + parse + `Validate`.
  - `func Parse(b []byte) (*Config, error)`.
  - `func (c *Config) Validate() error` — token non-empty, `AdminChatID != 0`, `Chats.Mode ∈ {auto, allowlist, owners_only}`, `Action ∈ {delete_mute, mute, ban, delete_only}`.

- [ ] **Step 1: Add dependency and test fixtures**

Run: `./scripts/dev.sh get gopkg.in/yaml.v3@v3.0.1 && make tidy`

`internal/config/testdata/valid.yaml`:
```yaml
bot_token: "12345:AA"
admin_chat_id: -1009999
action: delete_mute
chats:
  mode: auto
  start_in_dry_run: true
  allowlist: []
```

`internal/config/testdata/bad_action.yaml`:
```yaml
bot_token: "12345:AA"
admin_chat_id: -1009999
action: nuke
chats:
  mode: auto
```

- [ ] **Step 2: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import "testing"

func TestLoadValid(t *testing.T) {
	c, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminChatID != -1009999 || c.Chats.Mode != "auto" || !c.Chats.StartInDryRun {
		t.Fatalf("unexpected config: %+v", c)
	}
}

func TestValidateRejectsBadAction(t *testing.T) {
	if _, err := Load("testdata/bad_action.yaml"); err == nil {
		t.Fatal("expected validation error for action: nuke")
	}
}

func TestValidateRejectsMissingToken(t *testing.T) {
	_, err := Parse([]byte("admin_chat_id: -1\naction: mute\nchats:\n  mode: auto\n"))
	if err == nil {
		t.Fatal("expected error for empty bot_token")
	}
}
```

- [ ] **Step 3: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `Load`, `Parse`.

- [ ] **Step 4: Implement**

`internal/config/config.go`:
```go
// Package config loads and validates the YAML config that is the bot's single
// source of truth (spec §10).
package config

import (
	"fmt"
	"os"

	"github.com/stufently/telegram-antispam/internal/domain"
	"gopkg.in/yaml.v3"
)

type ChatsPolicy struct {
	Mode          string  `yaml:"mode"`
	StartInDryRun bool    `yaml:"start_in_dry_run"`
	Allowlist     []int64 `yaml:"allowlist"`
}

type Config struct {
	BotToken    string        `yaml:"bot_token"`
	AdminChatID int64         `yaml:"admin_chat_id"`
	Action      domain.Action `yaml:"action"`
	Chats       ChatsPolicy   `yaml:"chats"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.BotToken == "" {
		return fmt.Errorf("bot_token is required")
	}
	if c.AdminChatID == 0 {
		return fmt.Errorf("admin_chat_id is required")
	}
	switch c.Chats.Mode {
	case "auto", "allowlist", "owners_only":
	default:
		return fmt.Errorf("chats.mode must be auto|allowlist|owners_only, got %q", c.Chats.Mode)
	}
	switch c.Action {
	case domain.ActionDeleteMute, domain.ActionMute, domain.ActionBan, domain.ActionDeleteOnly:
	default:
		return fmt.Errorf("action must be delete_mute|mute|ban|delete_only, got %q", c.Action)
	}
	return nil
}
```

- [ ] **Step 5: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config go.mod go.sum
git commit -m "Add config schema, load, and validation"
```

---

### Task 6: Atomic config hot-reload

**Files:**
- Create: `internal/config/reload.go`
- Test: `internal/config/reload_test.go`

**Interfaces:**
- Consumes: `internal/config` (Task 5), `github.com/fsnotify/fsnotify`.
- Produces:
  - `type Store struct { ... }` with `func NewStore(initial *Config) *Store`, `func (s *Store) Current() *Config`, and `func (s *Store) Swap(candidate *Config)`.
  - `func (s *Store) tryReload(path string) error` — parse+validate a candidate; on success `Swap`, on failure return the error and keep the old config.
  - `Swap` is atomic (guarded by a mutex; `Current` never returns a half-parsed config).

- [ ] **Step 1: Add dependency**

Run: `./scripts/dev.sh get github.com/fsnotify/fsnotify@v1.7.0 && make tidy`

- [ ] **Step 2: Write the failing test**

`internal/config/reload_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func base(action string) *Config {
	return &Config{BotToken: "t", AdminChatID: -1, Action: Action(action), Chats: ChatsPolicy{Mode: "auto"}}
}

func TestReloadKeepsOldConfigOnInvalidFile(t *testing.T) {
	s := NewStore(base("mute"))
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("action: nuke\nchats:\n  mode: auto\n"), 0o600)

	if err := s.tryReload(p); err == nil {
		t.Fatal("expected reload to reject invalid file")
	}
	if s.Current().Action != ActionMute {
		t.Fatalf("running config changed on bad reload: %v", s.Current().Action)
	}
}

func TestReloadSwapsOnValidFile(t *testing.T) {
	s := NewStore(base("mute"))
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"), 0o600)

	if err := s.tryReload(p); err != nil {
		t.Fatal(err)
	}
	if s.Current().Action != ActionBan {
		t.Fatalf("config not swapped, got %v", s.Current().Action)
	}
}
```

> Note: this test references `Action(...)` and `ActionMute`/`ActionBan` — add a
> local alias in this package so the test reads cleanly. See Step 4.

- [ ] **Step 3: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `NewStore`, `Action`, `ActionMute`.

- [ ] **Step 4: Implement**

`internal/config/reload.go`:
```go
package config

import (
	"sync"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// Action re-exports the domain enum so config-package tests and callers can
// refer to it without importing domain directly.
type Action = domain.Action

const (
	ActionDeleteMute = domain.ActionDeleteMute
	ActionMute       = domain.ActionMute
	ActionBan        = domain.ActionBan
	ActionDeleteOnly = domain.ActionDeleteOnly
)

// Store holds the live config and swaps it atomically on reload.
type Store struct {
	mu  sync.RWMutex
	cur *Config
}

func NewStore(initial *Config) *Store { return &Store{cur: initial} }

// Current returns the live config. Never returns a partially-parsed value.
func (s *Store) Current() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Swap replaces the live config.
func (s *Store) Swap(candidate *Config) {
	s.mu.Lock()
	s.cur = candidate
	s.mu.Unlock()
}

// tryReload parses+validates path; on success swaps, on failure keeps the old
// config and returns the error.
func (s *Store) tryReload(path string) error {
	candidate, err := Load(path)
	if err != nil {
		return err
	}
	s.Swap(candidate)
	return nil
}
```

- [ ] **Step 5: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config go.mod go.sum
git commit -m "Add atomic config hot-reload store"
```

---

### Task 7: SQLite engine with single writer

**Files:**
- Create: `internal/store/engine.go`
- Test: `internal/store/engine_test.go`

**Interfaces:**
- Consumes: `modernc.org/sqlite`.
- Produces:
  - `func Open(path string) (*DB, error)` — opens with the mandated pragmas in the DSN, verifies `PRAGMA journal_mode` returns `wal`.
  - `type DB struct { ... }` wrapping `*sql.DB`.
  - `func (db *DB) Write(fn func(*sql.Tx) error) error` — runs `fn` in a transaction, serialized through a single writer goroutine.
  - `func (db *DB) Read() *sql.DB` — for read queries.
  - `func (db *DB) Close() error`.

- [ ] **Step 1: Add dependency**

Run: `./scripts/dev.sh get modernc.org/sqlite@v1.34.4 && make tidy`

- [ ] **Step 2: Write the failing test**

`internal/store/engine_test.go`:
```go
package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenEnablesWAL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.Read().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestWriteSerializesConcurrentWriters(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec("CREATE TABLE c(n INTEGER)")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	db.Write(func(tx *sql.Tx) error { _, e := tx.Exec("INSERT INTO c(n) VALUES(0)"); return e })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.Write(func(tx *sql.Tx) error {
				_, e := tx.Exec("UPDATE c SET n = n + 1")
				return e
			})
		}()
	}
	wg.Wait()
	var n int
	db.Read().QueryRow("SELECT n FROM c").Scan(&n)
	if n != 50 {
		t.Fatalf("n = %d, want 50 (writes lost to a race)", n)
	}
}
```

- [ ] **Step 3: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `Open`.

- [ ] **Step 4: Implement**

`internal/store/engine.go`:
```go
// Package store is the SQLite persistence layer: WAL, one writer goroutine.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type writeReq struct {
	fn   func(*sql.Tx) error
	done chan error
}

// DB wraps *sql.DB and serializes writes through one goroutine.
type DB struct {
	sql    *sql.DB
	writes chan writeReq
	quit   chan struct{}
}

// Open opens the database with the mandated pragmas and starts the writer.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		path,
	)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	db := &DB{sql: sqlDB, writes: make(chan writeReq), quit: make(chan struct{})}
	go db.writer()
	return db, nil
}

func (db *DB) writer() {
	for {
		select {
		case req := <-db.writes:
			req.done <- db.runTx(req.fn)
		case <-db.quit:
			return
		}
	}
}

func (db *DB) runTx(fn func(*sql.Tx) error) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Write runs fn in a transaction on the single writer goroutine.
func (db *DB) Write(fn func(*sql.Tx) error) error {
	req := writeReq{fn: fn, done: make(chan error, 1)}
	db.writes <- req
	return <-req.done
}

// Read returns the underlying DB for read queries (WAL allows concurrent reads).
func (db *DB) Read() *sql.DB { return db.sql }

// Close stops the writer and closes the database.
func (db *DB) Close() error {
	close(db.quit)
	return db.sql.Close()
}
```

- [ ] **Step 5: Run the test, expect pass**

Run: `./scripts/dev.sh test -race ./internal/store/`
Expected: PASS with no race warnings.

- [ ] **Step 6: Commit**

```bash
git add internal/store go.mod go.sum
git commit -m "Add sqlite engine with single writer"
```

---

### Task 8: Schema migrations

**Files:**
- Create: `internal/store/migrate.go`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: `internal/store` (Task 7).
- Produces: `func (db *DB) Migrate() error` — idempotent; creates tables `updates`, `chats`, `chat_aliases`, `incidents`, `evidence`, `audit`, `samples` with the keys from spec §9. Safe to call repeatedly.

- [ ] **Step 1: Write the failing test**

`internal/store/migrate_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}
	for _, tbl := range []string{"updates", "chats", "chat_aliases", "incidents", "evidence", "audit", "samples"} {
		var name string
		err := db.Read().QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing: %v", tbl, err)
		}
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `Migrate`.

- [ ] **Step 3: Implement**

`internal/store/migrate.go`:
```go
package store

import "database/sql"

const schema = `
CREATE TABLE IF NOT EXISTS updates (
	update_id INTEGER PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS chats (
	chat_id     INTEGER PRIMARY KEY,
	enabled     INTEGER NOT NULL DEFAULT 1,
	dry_run     INTEGER NOT NULL DEFAULT 1,
	linked_chat_id INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS chat_aliases (
	old_id INTEGER PRIMARY KEY,
	new_id INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS incidents (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id      INTEGER NOT NULL,
	message_id   INTEGER NOT NULL,
	user_id      INTEGER NOT NULL DEFAULT 0,
	state        TEXT    NOT NULL,
	dry_run      INTEGER NOT NULL DEFAULT 1,
	created_at   INTEGER NOT NULL DEFAULT (strftime('%s','now')),
	UNIQUE(chat_id, message_id)
);
CREATE TABLE IF NOT EXISTS evidence (
	incident_id      INTEGER NOT NULL,
	admin_chat_id    INTEGER NOT NULL,
	admin_message_id INTEGER NOT NULL,
	FOREIGN KEY(incident_id) REFERENCES incidents(id)
);
CREATE TABLE IF NOT EXISTS audit (
	incident_id INTEGER NOT NULL,
	action      TEXT    NOT NULL,
	scope       TEXT    NOT NULL,
	reason      TEXT    NOT NULL,
	signals     TEXT    NOT NULL,
	created_at  INTEGER NOT NULL DEFAULT (strftime('%s','now'))
);
CREATE TABLE IF NOT EXISTS samples (
	scope           TEXT NOT NULL,
	label           TEXT NOT NULL,
	origin          TEXT NOT NULL,
	normalized_hash TEXT NOT NULL,
	UNIQUE(scope, label, normalized_hash)
);
`

// Migrate creates all tables if absent. It is idempotent.
func (db *DB) Migrate() error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(schema)
		return err
	})
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add schema migrations"
```

---

### Task 9: Updates repository (idempotency)

**Files:**
- Create: `internal/store/updates.go`
- Test: `internal/store/updates_test.go`

**Interfaces:**
- Consumes: `internal/store` (Tasks 7–8).
- Produces: `func (db *DB) MarkUpdateSeen(updateID int64) (fresh bool, err error)` — inserts the id; returns `fresh=true` the first time, `fresh=false` on a duplicate (so the caller skips reprocessing).

- [ ] **Step 1: Write the failing test**

`internal/store/updates_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func TestMarkUpdateSeenDedup(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.MarkUpdateSeen(1001)
	if err != nil || !fresh {
		t.Fatalf("first: fresh=%v err=%v", fresh, err)
	}
	fresh, err = db.MarkUpdateSeen(1001)
	if err != nil || fresh {
		t.Fatalf("duplicate: fresh=%v err=%v (want fresh=false)", fresh, err)
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `MarkUpdateSeen`.

- [ ] **Step 3: Implement**

`internal/store/updates.go`:
```go
package store

import "database/sql"

// MarkUpdateSeen records update_id. Returns fresh=true only the first time an
// id is seen; a duplicate returns fresh=false so the caller skips it.
func (db *DB) MarkUpdateSeen(updateID int64) (bool, error) {
	var fresh bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec("INSERT OR IGNORE INTO updates(update_id) VALUES(?)", updateID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		fresh = n == 1
		return nil
	})
	return fresh, err
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add updates repo for idempotency"
```

---

### Task 10: Chats repository (lifecycle and alias)

**Files:**
- Create: `internal/store/chats.go`
- Test: `internal/store/chats_test.go`

**Interfaces:**
- Consumes: `internal/store` (Tasks 7–8).
- Produces:
  - `type ChatRow struct { ChatID int64; Enabled bool; DryRun bool; LinkedChatID int64 }`.
  - `func (db *DB) UpsertChat(row ChatRow) error`.
  - `func (db *DB) GetChat(chatID int64) (ChatRow, bool, error)`.
  - `func (db *DB) DisableChat(chatID int64) error`.
  - `func (db *DB) AddAlias(oldID, newID int64) error` and `func (db *DB) ResolveChat(id int64) int64` (follows an alias, else returns id).

- [ ] **Step 1: Write the failing test**

`internal/store/chats_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func newMigrated(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestChatUpsertGetDisable(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	if err := db.UpsertChat(ChatRow{ChatID: -100123, Enabled: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	row, ok, err := db.GetChat(-100123)
	if err != nil || !ok || !row.DryRun {
		t.Fatalf("get: row=%+v ok=%v err=%v", row, ok, err)
	}
	if err := db.DisableChat(-100123); err != nil {
		t.Fatal(err)
	}
	row, _, _ = db.GetChat(-100123)
	if row.Enabled {
		t.Fatal("chat should be disabled")
	}
}

func TestResolveAlias(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	if err := db.AddAlias(-100, -200); err != nil {
		t.Fatal(err)
	}
	if got := db.ResolveChat(-100); got != -200 {
		t.Fatalf("resolve = %d, want -200", got)
	}
	if got := db.ResolveChat(-999); got != -999 {
		t.Fatalf("resolve unknown = %d, want -999", got)
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `UpsertChat`, `ChatRow`, etc.

- [ ] **Step 3: Implement**

`internal/store/chats.go`:
```go
package store

import "database/sql"

type ChatRow struct {
	ChatID       int64
	Enabled      bool
	DryRun       bool
	LinkedChatID int64
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (db *DB) UpsertChat(row ChatRow) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO chats(chat_id, enabled, dry_run, linked_chat_id)
VALUES(?,?,?,?)
ON CONFLICT(chat_id) DO UPDATE SET
	enabled=excluded.enabled,
	dry_run=excluded.dry_run,
	linked_chat_id=excluded.linked_chat_id`,
			row.ChatID, b2i(row.Enabled), b2i(row.DryRun), row.LinkedChatID)
		return err
	})
}

func (db *DB) GetChat(chatID int64) (ChatRow, bool, error) {
	var r ChatRow
	var en, dry int
	err := db.Read().QueryRow(
		"SELECT chat_id, enabled, dry_run, linked_chat_id FROM chats WHERE chat_id=?", chatID,
	).Scan(&r.ChatID, &en, &dry, &r.LinkedChatID)
	if err == sql.ErrNoRows {
		return ChatRow{}, false, nil
	}
	if err != nil {
		return ChatRow{}, false, err
	}
	r.Enabled, r.DryRun = en == 1, dry == 1
	return r, true, nil
}

func (db *DB) DisableChat(chatID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE chats SET enabled=0 WHERE chat_id=?", chatID)
		return err
	})
}

func (db *DB) AddAlias(oldID, newID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"INSERT OR REPLACE INTO chat_aliases(old_id, new_id) VALUES(?,?)", oldID, newID)
		return err
	})
}

// ResolveChat follows an alias if present, else returns id unchanged.
func (db *DB) ResolveChat(id int64) int64 {
	var newID int64
	err := db.Read().QueryRow("SELECT new_id FROM chat_aliases WHERE old_id=?", id).Scan(&newID)
	if err != nil {
		return id
	}
	return newID
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add chats repo with lifecycle and alias"
```

---

### Task 11: Incidents repository

**Files:**
- Create: `internal/store/incidents.go`
- Test: `internal/store/incidents_test.go`

**Interfaces:**
- Consumes: `internal/store` (Tasks 7–8), `internal/domain`.
- Produces:
  - `func (db *DB) InsertPending(chatID int64, messageID int, userID int64, dryRun bool) (id int64, fresh bool, err error)` — `INSERT OR IGNORE` on `UNIQUE(chat_id, message_id)`; `fresh=false` if the incident already existed (returns the existing id).
  - `func (db *DB) SetIncidentState(id int64, state domain.IncidentState) error`.
  - `func (db *DB) AddEvidence(incidentID int64, adminChatID int64, adminMessageIDs []int) error`.
  - `func (db *DB) GetIncidentState(id int64) (domain.IncidentState, error)`.

- [ ] **Step 1: Write the failing test**

`internal/store/incidents_test.go`:
```go
package store

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestInsertPendingDedupAndAdvance(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	id, fresh, err := db.InsertPending(-100123, 55, 7, true)
	if err != nil || !fresh || id == 0 {
		t.Fatalf("insert: id=%d fresh=%v err=%v", id, fresh, err)
	}
	id2, fresh2, err := db.InsertPending(-100123, 55, 7, true)
	if err != nil || fresh2 || id2 != id {
		t.Fatalf("dup insert: id=%d fresh=%v err=%v (want same id, fresh=false)", id2, fresh2, err)
	}

	if err := db.SetIncidentState(id, domain.StateEvidenced); err != nil {
		t.Fatal(err)
	}
	st, err := db.GetIncidentState(id)
	if err != nil || st != domain.StateEvidenced {
		t.Fatalf("state=%v err=%v", st, err)
	}

	if err := db.AddEvidence(id, -1009999, []int{111, 112}); err != nil {
		t.Fatal(err)
	}
	var n int
	db.Read().QueryRow("SELECT COUNT(*) FROM evidence WHERE incident_id=?", id).Scan(&n)
	if n != 2 {
		t.Fatalf("evidence rows = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `InsertPending`.

- [ ] **Step 3: Implement**

`internal/store/incidents.go`:
```go
package store

import (
	"database/sql"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// InsertPending inserts a pending incident keyed by (chat_id, message_id).
// On a duplicate it returns the existing id and fresh=false.
func (db *DB) InsertPending(chatID int64, messageID int, userID int64, dryRun bool) (int64, bool, error) {
	var id int64
	var fresh bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
INSERT OR IGNORE INTO incidents(chat_id, message_id, user_id, state, dry_run)
VALUES(?,?,?,?,?)`,
			chatID, messageID, userID, string(domain.StatePending), b2i(dryRun))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			fresh = true
			id, err = res.LastInsertId()
			return err
		}
		// existing row: fetch its id
		return tx.QueryRow(
			"SELECT id FROM incidents WHERE chat_id=? AND message_id=?", chatID, messageID,
		).Scan(&id)
	})
	return id, fresh, err
}

func (db *DB) SetIncidentState(id int64, state domain.IncidentState) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE incidents SET state=? WHERE id=?", string(state), id)
		return err
	})
}

func (db *DB) GetIncidentState(id int64) (domain.IncidentState, error) {
	var s string
	err := db.Read().QueryRow("SELECT state FROM incidents WHERE id=?", id).Scan(&s)
	return domain.IncidentState(s), err
}

func (db *DB) AddEvidence(incidentID int64, adminChatID int64, adminMessageIDs []int) error {
	return db.Write(func(tx *sql.Tx) error {
		for _, mid := range adminMessageIDs {
			if _, err := tx.Exec(
				"INSERT INTO evidence(incident_id, admin_chat_id, admin_message_id) VALUES(?,?,?)",
				incidentID, adminChatID, mid,
			); err != nil {
				return err
			}
		}
		return nil
	})
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add incidents repo"
```

---

### Task 12: Incident state machine (evidence before action)

**Files:**
- Create: `internal/incident/machine.go`
- Test: `internal/incident/machine_test.go`

**Interfaces:**
- Consumes: `internal/domain`, `internal/telegram`, `internal/store`, `internal/telegram/fake`.
- Produces:
  - `type Repo interface` mirroring the store methods the machine needs: `InsertPending(chatID int64, messageID int, userID int64, dryRun bool) (int64, bool, error)`, `SetIncidentState(id int64, s domain.IncidentState) error`, `AddEvidence(id int64, adminChatID int64, adminMessageIDs []int) error`. (`*store.DB` satisfies it.)
  - `type Machine struct { ... }` with `func New(port telegram.Port, repo Repo, adminChatID int64) *Machine`.
  - `func (m *Machine) Handle(ctx context.Context, inc domain.Incident) error` — runs the spec §7 ordering: insert pending → copy evidence → save admin ids (evidenced) → apply action if not dry-run (acted) → delete originals if not dry-run (cleaned) → done. On evidence-copy failure with a low-confidence verdict, it stops at `evidence_failed` and performs no destructive action.

- [ ] **Step 1: Write the failing test**

`internal/incident/machine_test.go`:
```go
package incident

import (
	"context"
	"errors"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// stubRepo is a minimal in-memory Repo.
type stubRepo struct{ state domain.IncidentState }

func (r *stubRepo) InsertPending(int64, int, int64, bool) (int64, bool, error) { return 1, true, nil }
func (r *stubRepo) SetIncidentState(_ int64, s domain.IncidentState) error     { r.state = s; return nil }
func (r *stubRepo) AddEvidence(int64, int64, []int) error                      { return nil }

func liveIncident(dry bool) domain.Incident {
	return domain.Incident{
		ChatID: -100123, MessageIDs: []int{55}, DryRun: dry,
		Sender:  domain.Sender{UserID: 7, Kind: domain.SenderUser},
		Verdict: domain.Verdict{Action: domain.ActionBan, Confidence: 0.99},
	}
}

func TestEvidenceBeforeActionAndDelete(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{}, 999)
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	// evidence copy + admin summary must precede the ban and the delete.
	idx := map[string]int{}
	for i, c := range calls {
		if _, ok := idx[c]; !ok {
			idx[c] = i
		}
	}
	if !(idx["CopyMessages"] < idx["BanMember"] && idx["SendAdmin"] < idx["BanMember"]) {
		t.Fatalf("evidence must precede ban; calls=%v", calls)
	}
	if !(idx["BanMember"] < idx["DeleteMessages"]) {
		t.Fatalf("ban must precede delete; calls=%v", calls)
	}
}

func TestDryRunSkipsDestructiveCalls(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{}, 999)
	if err := m.Handle(context.Background(), liveIncident(true)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "BanMember" || c == "DeleteMessages" || c == "RestrictMember" {
			t.Fatalf("dry-run performed destructive call %q; calls=%v", c, f.Calls())
		}
	}
}

func TestEvidenceFailureStopsBeforeAction(t *testing.T) {
	f := fake.New()
	f.CopyErr = errors.New("copy failed")
	repo := &stubRepo{}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.Verdict.Confidence = 0.4 // low confidence
	_ = m.Handle(context.Background(), inc)
	for _, c := range f.Calls() {
		if c == "BanMember" || c == "DeleteMessages" {
			t.Fatalf("must not act after evidence failure on low confidence; calls=%v", f.Calls())
		}
	}
	if repo.state != domain.StateEvidenceFailed {
		t.Fatalf("state = %v, want evidence_failed", repo.state)
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `New`, `Machine`.

- [ ] **Step 3: Implement**

`internal/incident/machine.go`:
```go
// Package incident runs the side-effecting state machine (spec §7). Ordering
// here is a Telegram API requirement: evidence is copied before any
// destructive action, because banning in a supergroup deletes prior messages.
package incident

import (
	"context"
	"fmt"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// Repo is the persistence surface the machine needs; *store.DB satisfies it.
type Repo interface {
	InsertPending(chatID int64, messageID int, userID int64, dryRun bool) (int64, bool, error)
	SetIncidentState(id int64, s domain.IncidentState) error
	AddEvidence(id int64, adminChatID int64, adminMessageIDs []int) error
}

// hardConfidence is the floor above which we still act even if evidence copy
// fails (a hard deny keeps metadata and blocks); below it we stop.
const hardConfidence = 0.9

type Machine struct {
	port        telegram.Port
	repo        Repo
	adminChatID int64
}

func New(port telegram.Port, repo Repo, adminChatID int64) *Machine {
	return &Machine{port: port, repo: repo, adminChatID: adminChatID}
}

func (m *Machine) Handle(ctx context.Context, inc domain.Incident) error {
	if len(inc.MessageIDs) == 0 {
		return fmt.Errorf("incident has no message ids")
	}
	id, _, err := m.repo.InsertPending(inc.ChatID, inc.MessageIDs[0], inc.Sender.UserID, inc.DryRun)
	if err != nil {
		return fmt.Errorf("insert pending: %w", err)
	}

	// 1. evidence BEFORE any destructive action.
	adminIDs, copyErr := m.port.CopyMessages(ctx, m.adminChatID, inc.ChatID, inc.MessageIDs)
	if copyErr != nil {
		if inc.Verdict.Confidence < hardConfidence {
			_ = m.repo.SetIncidentState(id, domain.StateEvidenceFailed)
			return fmt.Errorf("evidence copy failed, not acting on low confidence: %w", copyErr)
		}
		// hard deny: proceed without copied evidence but record the failure.
		_ = m.repo.SetIncidentState(id, domain.StateEvidenceFailed)
	} else {
		if _, err := m.port.SendAdmin(ctx, m.adminChatID, telegram.AdminMessage{
			IncidentKey:      fmt.Sprintf("%d", id),
			SourceChatID:     inc.ChatID,
			CopiedFromChatID: inc.ChatID,
			CopyMessageIDs:   inc.MessageIDs,
			Text:             inc.Verdict.Reason,
		}); err != nil {
			return fmt.Errorf("send admin: %w", err)
		}
		if err := m.repo.AddEvidence(id, m.adminChatID, adminIDs); err != nil {
			return fmt.Errorf("save evidence: %w", err)
		}
		_ = m.repo.SetIncidentState(id, domain.StateEvidenced)
	}

	if inc.DryRun {
		return m.repo.SetIncidentState(id, domain.StateDone)
	}

	// 2. apply action.
	if err := m.applyAction(ctx, inc); err != nil {
		return fmt.Errorf("apply action: %w", err)
	}
	_ = m.repo.SetIncidentState(id, domain.StateActed)

	// 3. delete originals last.
	if err := m.port.DeleteMessages(ctx, inc.ChatID, inc.MessageIDs); err != nil {
		return fmt.Errorf("delete originals: %w", err)
	}
	_ = m.repo.SetIncidentState(id, domain.StateCleaned)

	return m.repo.SetIncidentState(id, domain.StateDone)
}

func (m *Machine) applyAction(ctx context.Context, inc domain.Incident) error {
	switch inc.Verdict.Action {
	case domain.ActionBan:
		return m.port.BanMember(ctx, inc.ChatID, inc.Sender.UserID)
	case domain.ActionMute, domain.ActionDeleteMute:
		return m.port.RestrictMember(ctx, inc.ChatID, inc.Sender.UserID, telegram.Perms{CanSend: false}, 0)
	case domain.ActionDeleteOnly, domain.ActionQuarantine, domain.ActionNone:
		return nil
	default:
		return fmt.Errorf("unknown action %q", inc.Verdict.Action)
	}
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Verify *store.DB satisfies Repo**

Add to `internal/store/incidents.go` (a compile-time assertion lives with the machine to avoid an import cycle — put it in the incident package instead):

`internal/incident/repo_assert.go`:
```go
package incident

import "github.com/stufently/telegram-antispam/internal/store"

var _ Repo = (*store.DB)(nil)
```

Run: `make build`
Expected: compiles (proves the real store satisfies the machine's Repo).

- [ ] **Step 6: Commit**

```bash
git add internal/incident
git commit -m "Add incident state machine"
```

---

### Task 13: Telegram adapter — update to domain

**Files:**
- Create: `internal/telegram/adapter.go`
- Test: `internal/telegram/adapter_test.go`

**Interfaces:**
- Consumes: `github.com/go-telegram/bot/models`, `internal/domain`, `internal/detect`.
- Produces: `func ToDomainMessage(m *models.Message) domain.Message` — maps a library message to `domain.Message`, filling `Sender` via `detect.ClassifySender`, and copying `ChatID`, `MessageID`, `ThreadID` (`MessageThreadID`), `MediaGroupID`, `Text`/`Caption`, `Date`, `IsAutomaticForward`.

- [ ] **Step 1: Add the library**

Run: `./scripts/dev.sh get github.com/go-telegram/bot@v1.23.0 && make tidy`

- [ ] **Step 2: Write the failing test**

`internal/telegram/adapter_test.go`:
```go
package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestToDomainMessagePlainUser(t *testing.T) {
	m := &models.Message{
		ID:     55,
		Chat:   models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From:   &models.User{ID: 7, Username: "bob", FirstName: "Bob"},
		Text:   "hello",
		Date:   1700,
	}
	got := ToDomainMessage(m)
	if got.ChatID != -100123 || got.MessageID != 55 || got.Text != "hello" {
		t.Fatalf("bad envelope: %+v", got)
	}
	if got.Sender.Kind != domain.SenderUser || got.Sender.UserID != 7 || got.Sender.Username != "bob" {
		t.Fatalf("bad sender: %+v", got.Sender)
	}
}

func TestToDomainMessageExternalChannel(t *testing.T) {
	m := &models.Message{
		ID:         9,
		Chat:       models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		SenderChat: &models.Chat{ID: -100888, Type: models.ChatTypeChannel},
	}
	got := ToDomainMessage(m)
	if got.Sender.Kind != domain.SenderExternalChannel || got.Sender.SenderChatID != -100888 {
		t.Fatalf("bad sender: %+v", got.Sender)
	}
}
```

- [ ] **Step 3: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `ToDomainMessage`.

- [ ] **Step 4: Implement**

`internal/telegram/adapter.go`:
```go
package telegram

import (
	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
)

// ToDomainMessage maps a library message to the domain envelope, classifying
// the sender. This is the boundary where library types stop.
func ToDomainMessage(m *models.Message) domain.Message {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	in := detect.ClassifyInput{
		ChatID:             m.Chat.ID,
		IsAutomaticForward: m.IsAutomaticForward,
	}
	sender := domain.Sender{}
	if m.From != nil {
		in.FromID = m.From.ID
		in.IsBot = m.From.IsBot
		sender.UserID = m.From.ID
		sender.Username = m.From.Username
		sender.DisplayName = m.From.FirstName
	}
	if m.SenderChat != nil {
		in.SenderChatID = m.SenderChat.ID
		in.SenderChatType = string(m.SenderChat.Type)
		sender.SenderChatID = m.SenderChat.ID
	}
	sender.Kind = detect.ClassifySender(in)

	return domain.Message{
		ChatID:             m.Chat.ID,
		MessageID:          m.ID,
		ThreadID:           m.MessageThreadID,
		MediaGroupID:       m.MediaGroupID,
		Sender:             sender,
		Text:               text,
		Date:               int64(m.Date),
		IsAutomaticForward: m.IsAutomaticForward,
	}
}
```

> If a field name (e.g. `MessageThreadID`, `MediaGroupID`, `IsAutomaticForward`)
> differs in v1.23.0, run `./scripts/dev.sh doc github.com/go-telegram/bot/models.Message`
> and adjust — the mapping is mechanical.

- [ ] **Step 5: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/telegram go.mod go.sum
git commit -m "Add telegram adapter to domain message"
```

---

### Task 14: Per-chat sequencer

**Files:**
- Create: `internal/telegram/sequencer.go`
- Test: `internal/telegram/sequencer_test.go`

**Interfaces:**
- Consumes: nothing beyond stdlib.
- Produces:
  - `type Sequencer struct { ... }` with `func NewSequencer() *Sequencer`.
  - `func (s *Sequencer) Submit(chatID int64, job func())` — jobs for the same `chatID` run in submission order on one goroutine; different chats run concurrently.
  - `func (s *Sequencer) Wait()` — drains all queues (for tests/shutdown).

- [ ] **Step 1: Write the failing test**

`internal/telegram/sequencer_test.go`:
```go
package telegram

import (
	"sync"
	"testing"
)

func TestSequencerOrdersPerChat(t *testing.T) {
	s := NewSequencer()
	var mu sync.Mutex
	seq := []int{}
	for i := 0; i < 100; i++ {
		i := i
		s.Submit(-100123, func() {
			mu.Lock()
			seq = append(seq, i)
			mu.Unlock()
		})
	}
	s.Wait()
	for i := range seq {
		if seq[i] != i {
			t.Fatalf("out of order at %d: %v", i, seq[:i+1])
		}
	}
}

func TestSequencerRunsDistinctChats(t *testing.T) {
	s := NewSequencer()
	done := make(chan int64, 2)
	s.Submit(-1, func() { done <- -1 })
	s.Submit(-2, func() { done <- -2 })
	s.Wait()
	close(done)
	got := map[int64]bool{}
	for id := range done {
		got[id] = true
	}
	if !got[-1] || !got[-2] {
		t.Fatalf("both chats should run, got %v", got)
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `NewSequencer`.

- [ ] **Step 3: Implement**

`internal/telegram/sequencer.go`:
```go
package telegram

import "sync"

// Sequencer serializes jobs per chat_id while running different chats
// concurrently, so updates within one chat are processed in order.
type Sequencer struct {
	mu     sync.Mutex
	queues map[int64]chan func()
	wg     sync.WaitGroup
}

func NewSequencer() *Sequencer {
	return &Sequencer{queues: make(map[int64]chan func())}
}

func (s *Sequencer) Submit(chatID int64, job func()) {
	s.mu.Lock()
	q, ok := s.queues[chatID]
	if !ok {
		q = make(chan func(), 1024)
		s.queues[chatID] = q
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for j := range q {
				j()
			}
		}()
	}
	s.mu.Unlock()
	q <- job
}

// Wait closes all queues and waits for workers to drain. Call once at shutdown.
func (s *Sequencer) Wait() {
	s.mu.Lock()
	for _, q := range s.queues {
		close(q)
	}
	s.queues = make(map[int64]chan func())
	s.mu.Unlock()
	s.wg.Wait()
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `./scripts/dev.sh test -race ./internal/telegram/`
Expected: PASS, no race warnings.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram
git commit -m "Add per-chat sequencer"
```

---

### Task 15: Wire the dry-run delivery spine

**Files:**
- Create: `internal/telegram/bot.go`
- Create: `cmd/tg-antispam/main.go`
- Create: `config.example.yaml`
- Test: `internal/telegram/bot_test.go`

**Interfaces:**
- Consumes: everything above, `github.com/go-telegram/bot`.
- Produces:
  - `func RegisteredChat(cfg *config.Config, chatID int64) bool` — applies `chats.mode` (auto = any; allowlist = in `cfg.Chats.Allowlist`).
  - `type Handler struct { ... }` with `func NewHandler(db *store.DB, seq *Sequencer, cfg *config.Store, machine *incident.Machine) *Handler` (`*config.Store` is from Task 6) and `func (h *Handler) OnMessage(ctx context.Context, updateID int64, m domain.Message)` — dedups the update, skips unregistered/immune chats, and (M1 behaviour) records the chat and logs; it does not fabricate verdicts (detection arrives in M3). The incident machine is wired but only invoked by later milestones.
  - `main()` — load config, open+migrate store, build a live `bot` with the mandated `allowed_updates`, and run long polling. Runs but is guarded so `go vet`/tests don't require a token.

- [ ] **Step 1: Write the failing test**

`internal/telegram/bot_test.go`:
```go
package telegram

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestRegisteredChatModes(t *testing.T) {
	auto := &config.Config{Chats: config.ChatsPolicy{Mode: "auto"}}
	if !RegisteredChat(auto, -100123) {
		t.Error("auto mode should accept any chat")
	}
	allow := &config.Config{Chats: config.ChatsPolicy{Mode: "allowlist", Allowlist: []int64{-100123}}}
	if !RegisteredChat(allow, -100123) {
		t.Error("allowlisted chat should be accepted")
	}
	if RegisteredChat(allow, -100999) {
		t.Error("non-allowlisted chat should be rejected")
	}
}

func TestImmuneKinds(t *testing.T) {
	if !ImmuneSender(domain.Sender{Kind: domain.SenderAnonAdmin}) {
		t.Error("anon admin is immune")
	}
	if !ImmuneSender(domain.Sender{Kind: domain.SenderLinkedChannel}) {
		t.Error("linked channel is immune")
	}
	if ImmuneSender(domain.Sender{Kind: domain.SenderUser}) {
		t.Error("plain user is not immune")
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `make test`
Expected: FAIL — undefined `RegisteredChat`, `ImmuneSender`.

- [ ] **Step 3: Implement the routing helpers and handler**

`internal/telegram/bot.go`:
```go
package telegram

import (
	"context"
	"log"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
)

// RegisteredChat reports whether the bot should serve chatID under cfg.
func RegisteredChat(cfg *config.Config, chatID int64) bool {
	switch cfg.Chats.Mode {
	case "allowlist":
		for _, id := range cfg.Chats.Allowlist {
			if id == chatID {
				return true
			}
		}
		return false
	default: // auto, owners_only (owners handled at registration time)
		return true
	}
}

// ImmuneSender reports senders the moderation pipeline must never act on.
func ImmuneSender(s domain.Sender) bool {
	return s.Kind == domain.SenderAnonAdmin || s.Kind == domain.SenderLinkedChannel
}

// Handler is the M1 delivery spine: dedup, route, register chat. Detection and
// verdict-building arrive in later milestones; the incident machine is wired
// but invoked by those milestones.
type Handler struct {
	db      *store.DB
	seq     *Sequencer
	cfg     *config.Store
	machine *incident.Machine
}

func NewHandler(db *store.DB, seq *Sequencer, cfg *config.Store, machine *incident.Machine) *Handler {
	return &Handler{db: db, seq: seq, cfg: cfg, machine: machine}
}

// OnMessage is called by the poller for each message update.
func (h *Handler) OnMessage(ctx context.Context, updateID int64, m domain.Message) {
	fresh, err := h.db.MarkUpdateSeen(updateID)
	if err != nil {
		log.Printf("dedup update %d: %v", updateID, err)
		return
	}
	if !fresh {
		return
	}
	cfg := h.cfg.Current()
	if !RegisteredChat(cfg, m.ChatID) || ImmuneSender(m.Sender) {
		return
	}
	h.seq.Submit(m.ChatID, func() {
		_ = h.db.UpsertChat(store.ChatRow{
			ChatID:  m.ChatID,
			Enabled: true,
			DryRun:  cfg.Chats.StartInDryRun,
		})
		log.Printf("chat=%d msg=%d sender=%s: observed (dry-run spine)", m.ChatID, m.MessageID, m.Sender.Kind)
	})
}
```

- [ ] **Step 4: Run the test, expect pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Write the entry point**

`cmd/tg-antispam/main.go`:
```go
// Command tg-antispam is the single-process antispam bot.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/version"
)

func main() {
	log.Printf("tg-antispam %s starting", version.String())

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfgStore := config.NewStore(cfg)

	db, err := store.Open(os.Getenv("DB_PATH"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	seq := telegram.NewSequencer()
	defer seq.Wait()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var machine *incident.Machine // wired by later milestones via a live port
	handler := telegram.NewHandler(db, seq, cfgStore, machine)

	opts := []tgbot.Option{
		tgbot.WithAllowedUpdates([]string{
			"message", "edited_message", "callback_query",
			"chat_member", "my_chat_member", "message_reaction",
		}),
		tgbot.WithDefaultHandler(func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			if update.Message == nil {
				return
			}
			handler.OnMessage(ctx, int64(update.ID), telegram.ToDomainMessage(update.Message))
		}),
	}

	b, err := tgbot.New(cfg.BotToken, opts...)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}
	log.Print("long polling started")
	b.Start(ctx)
}
```

`config.example.yaml`:
```yaml
bot_token: "REPLACE_WITH_BOTFATHER_TOKEN"
admin_chat_id: -1000000000000
action: delete_mute
chats:
  mode: auto
  start_in_dry_run: true
  allowlist: []
```

- [ ] **Step 6: Verify build and vet**

Run: `make build && make vet`
Expected: both succeed. (The binary needs a real token+config at runtime; build/vet do not.)

> If `WithAllowedUpdates` / `WithDefaultHandler` / `b.Start` names differ in
> v1.23.0, check `./scripts/dev.sh doc github.com/go-telegram/bot` and adjust;
> the wiring shape is unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/telegram cmd config.example.yaml go.mod go.sum
git commit -m "Wire dry-run delivery spine and entry point"
```

---

## Milestone 1 Definition of Done

- `make test` (and `./scripts/dev.sh test -race ./...`) is green.
- `make build` produces the binary; `make vet` is clean.
- A process started with a valid token + config connects, subscribes with the
  mandated `allowed_updates`, dedups updates, registers chats in dry-run, and
  logs observed messages — no destructive action anywhere.
- The incident state machine enforces evidence-before-action and dry-run
  no-op, proven by tests against the fake port.
- Nothing was installed on the host; everything ran in `golang:1.26`.

**Next milestone (separate plan):** M2 — outbound queue with priorities and
rate limiting, real evidence copy + album buffering, the admin chat with the
four buttons and per-callback RBAC — which is where the incident machine gets
its live port and starts being invoked from `OnMessage`.
```
