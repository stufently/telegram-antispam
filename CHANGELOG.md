# Changelog

Notable changes to `tg-antispam`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
released versions match the Helm chart's `appVersion` (see `AGENTS.md`, "Change patterns").

Entries start life under **Unreleased** and are moved under a version heading when
`Chart.appVersion` is bumped and a `v*` tag is pushed.

## [Unreleased]

## [0.14.2] - 2026-08-25

### Fixed

- A sanction is no longer applied on evidence that never arrived. Telegram's
  `copyMessages` silently SKIPS messages it cannot copy — a quiz poll is the
  case seen in production, and a poll's option texts are part of what the
  detectors judge — and still reports success, so the call can return a short or empty
  id list with no error. An empty list was read as "evidence copied": the
  incident was marked `evidenced` and the sanction went ahead, leaving the
  admin chat with a verdict card, undo buttons and nothing underneath to
  review them against. It is now treated as the copy having failed and takes
  the existing `evidence_failed` branch — admins are told, and a probabilistic
  verdict (rules, behavior, Bayes, LLM) is not enforced; an externally
  verifiable blocklist hit still is, exactly as when the copy errors out.

### Changed

- A PARTIALLY copied album is still sanctioned — dropping the action would let
  one uncopyable part shield the whole album — but its card now says so:
  `evidence INCOMPLETE: copied N of M messages — the part that triggered the
  verdict may be missing`. An album is judged on the single part carrying its
  text and the copy result is destination ids with no mapping back, so the
  count of missing parts is known and their identity is not. Without the line
  a moderator reviewing a false positive reads the copies as the whole message
  and upholds or overturns the verdict on part of the picture.

## [0.14.1] - 2026-08-25

### Fixed

- The admin-chat verdict card is now sent as a **reply to the copied evidence**
  instead of as an unrelated follow-up message. Incidents from different source
  chats are processed in parallel and an album copies several messages for one
  card, so admin-chat order could legitimately read "evidence A, evidence B,
  card B, card A" — anyone reviewing a false positive by message order would
  then judge one incident's evidence against another's verdict, and unban a
  spammer or refuse to unban a person. The card anchors on the first copied
  message id (`AdminMessage.CopyMessageIDs[0]`), which is enough for an album.

  The card still goes out when the thread cannot be formed, because it is the
  only trace an incident leaves: with no evidence copy at all (the
  `StateEvidenceFailed` branch) it is sent unthreaded as before, it carries
  `allow_sending_without_reply`, and a Telegram refusal that names the reply
  target is retried once without the reply.
