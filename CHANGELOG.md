# Changelog

Notable changes to `tg-antispam`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
released versions match the Helm chart's `appVersion` (see `AGENTS.md`, "Change patterns").

Entries start life under **Unreleased** and are moved under a version heading when
`Chart.appVersion` is bumped and a `v*` tag is pushed.

## [Unreleased]

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
