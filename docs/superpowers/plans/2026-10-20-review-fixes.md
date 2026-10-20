# Moderation Review Fixes Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans; independent adapter and normalization fixes use superpowers:dispatching-parallel-agents.

**Goal:** Verify and fix seven reported moderation defects, then release a patch version.

**Architecture:** Preserve the one-process SQLite/Telegram architecture, evidence-first sanctions, per-chat sequencing and opt-in JEV. Resolve races through shared ordering and explicit incident state semantics rather than duplicate moderation paths.

**Tech Stack:** Existing Go 1.27.3 toolchain, SQLite, Docker test runner, Helm, GitHub release CI.

**Spec:** User's seven-item review and AGENTS.md invariants; console Claude consultation is recorded below.

## Global Constraints
- Reproduce each finding before fixing it, using local fakes and SQLite only.
- No raw message text persistence, no new paid API usage.
- Keep anonymous administrators immune, edits unable to earn trust, and training corrections exclusive.
- Accepted callback routing must drain before the source-chat sequencer closes.
- Preserve real/in-flight sanction protection when releasing orphan captcha mutes.
- Commit/push current branch and release next patch after checks.

## Review Focus
- Callback races with initial restriction, command training, and shutdown.
- Captcha without prompt versus real or in-flight sanctions.
- Evidence-failure state must never describe an operation still allowed to sanction.
- Hidden links, UTF-16 offsets and overlapping regex matches.
- Legitimate channel targets and ordinary albums remain supported.

### Task 1: Independent input guards and link counts
- [x] Reproduce/fix album edit marker and anonymous-admin target protection.
- [x] Reproduce/fix link occurrence counts; retain distinct repeated links and merge overlap.

### Task 2: Decision ordering, training and captcha
- [x] Consult console Claude on all findings and implementation tradeoffs.
- [x] Reproduce/fix callback ordering relative to initial moderation and manual training.
- [x] Wire callback learning through RelabelTokens and test both labels/scopes.
- [x] Reproduce/fix orphan captcha handling and evidence-failed sanction queries, preserving in-flight protection.

### Task 3: Verification and release
- [x] Update architecture, changelog and invariants as needed.
- [x] Run full test/vet/build/race/lint checks and fresh review; fix substantive findings.
- [ ] Bump chart/appVersion and changelog to patch release, commit/push/tag and verify GitHub artifacts/image.

## Execution record

- Initial worktree clean at v0.21.0.
- User authorized autonomous implementation and release; no further design approval needed.

- Console Claude verified all seven reports against the code. It additionally identified the same training defect in CLI `decide` and missing protection of linked-channel command targets; both were included with regression tests.
- Ruling: use source-chat callback sequencing rather than a new initial-enforcement claim. This waits for the whole command including training, avoids introducing crash-recovery claims, and permits a single early fp press to undo correctly. A separate routing queue keeps DB lookup off polling and drains before source queues. Failed scope lookup never executes without source ordering.
- Ruling: retain pending/evidenced and active enf claims conservatively for captcha; use explicit terminal not_acted for known completed failures. Do not equate a transient fp/lift claim with a completed undo. No schema migration: states use the existing TEXT column. Old crash-interrupted rows remain conservative.
- Regression evidence: album edit/target/link tests failed before their fixes; captcha orphan and evidence-failed tests failed before fixes; CLI decide relabel test failed with spam=1, ham=1; no-evidence in-flight tests failed with evidence_failed, completed failures with pending/evidenced; routing lookup error test failed before removing unsafe fallback.
- Integration tests verify fp during blocked RestrictMember waits until both sanction and command training finish, then unrestricts exactly once and leaves spam=0/ham=1. Routing drain preserves accepted callbacks and different chats still run independently.
- Verification: make test, make vet, make build, full test -race -count=1 ./..., golangci-lint 2.15.0 all passed. Independent fresh review found no Critical/Important issues. Corrected its minor test-fixture UTF-16 length observation and added adjacent-entity coverage; detect tests pass.

- Second console Claude review found a deletion-only enforcement retaining its claim and the relabel helper dropping CLI origin. Added reproductions (both red), release the claim when no sanction landed, and preserve origin atomically in relabeling.
- Final verification after these corrections: targeted admin/train/store tests, make test, make vet, make build, full race run with -count=1, golangci-lint (0 issues), Helm lint, and git diff --check passed. Release metadata prepared for v0.21.1.
