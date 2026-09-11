# Changelog

Notable changes to `tg-antispam`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
released versions match the Helm chart's `appVersion` (see `AGENTS.md`, "Change patterns").

Entries start life under **Unreleased** and are moved under a version heading when
`Chart.appVersion` is bumped and a `v*` tag is pushed.

## [Unreleased]

### Added

- Optional `detection.rules.allow_google_maps_links` (default false). When
  enabled, recognized Google Maps URLs alone no longer trigger
  `link_from_untrusted`. Other detectors still see the original links and can
  still sanction the message. Backslash path tricks, empty/non-numeric ports
  and a `..` path segment are not recognized as Maps.

## [0.16.0] - 2026-08-30

### Fixed

- A moderator's `/spam` or `/ban` is no longer discarded when the evidence copy
  into the admin chat fails. Enforcement without evidence was limited to
  externally verifiable verdicts, which was right for the detector and wrong
  for a human: the copy exists so that a person can check a machine verdict,
  and there is nothing to check when the person IS the verdict — they typed the
  command as a reply, looking at the message. The failure was total and had no
  way out: a quiz poll is not copyable (Telegram skips it and reports success),
  so `/spam` on one produced no ban, no delete and no complaint, a repeated
  `/spam` was rejected as already handled, and the evidence-failure card
  carries no enforce button by design. Automatic verdicts still fail closed,
  and the card still names an action only when one actually follows.
- Attachment type rules — the `.apk` block among them — no longer depend on how
  the sender chose to upload the file. Extensions and MIME types were read only
  from Telegram's `document`, but the Bot API puts `file_name` and `mime_type`
  on `video`, `animation` and `audio` as well, and `mime_type` on `voice`, so
  the same `list.apk` sent as a video reached no rule at all while
  `MediaKinds` had been listing those very types all along. All five fields are
  now read, symmetrically for a message and for an `external_reply` parent.
- The filename is now really discarded, not merely relabelled. `path.Ext`
  returns everything after the last dot, so a file called
  `отчёт.2 подробности в личку` yielded that whole sentence as its
  "extension" — persisted in the audit row and, with the LLM stage enabled,
  sent to the provider inside `[метаданные сообщения: ...]`, where
  sender-controlled text reads as an authoritative fact about the message. An
  extension is now kept only when it looks like one, and a MIME type only when
  it parses as `type/subtype` after its parameters are cut. Both checks live in
  the adapter, so the guarantee covers stored data and the LLM payload alike,
  and the parameter-stripping no longer exists only inside the rule that
  compares values.
- A validated shape was still not a type, and the sender picks the shape.
  Digits are legal in an extension, so `report.66812345678` passed validation
  and emitted a PHONE NUMBER as the file's "extension" — into the audit row and
  the LLM metadata line, which is the exact leak discarding the filename was
  meant to prevent. An extension now has to contain at least one ASCII letter,
  which every real one does (`.apk`, `.mp4`, `.7z`) and no version, date or
  contact number can. The MIME token alphabet is equally permissive: two words
  joined by a slash satisfy it, so `t.me/joinchat` passed as a "MIME type". The
  top-level half is now matched against IANA's closed registry of ten
  top-level types. The subtype cannot be checked this way — that registry is
  open — so the boundary guarantees a value shaped like a media type, not a
  true one.
- Two more attachment fields are now read for file types: `live_photo` (a MIME
  type, no file name, like `voice`) and the videos nested inside `paid_media`,
  which is a LIST whose video items carry `file_name` and `mime_type` one level
  down — a nil check on the field saw the attachment and read nothing out of
  it. `MediaKinds` had been listing both all along, so this was the same
  "the sender chooses which field the file arrives in" bypass as the video one.
  Both are read symmetrically for a message and for an `external_reply` parent.

### Changed

- The optional LLM stage now also sees the attachment types of the message a
  reply points at. A spammer split the payload from the pitch: the `.apk` went
  out alone, and a separate one-line reply ("Обновили наконец !") sold it. That
  reply carried no attachment, no link and no known words, so it reached the
  model as three innocuous words with nothing to point at and came back HAM.
  The judged message's own metadata line is unchanged; a second line
  (`[сообщение, на которое отвечают: ...]`) is added only when the parent
  actually carries an attachment. The parent's TEXT is never sent — it belongs
  to another person, and what makes the comment spam is the kind of file above
  it, not the words in it.
- That covers BOTH shapes of reply, which matters because the case that got
  through was the second one: the `.apk` had been posted in a private channel,
  and a reply across chats arrives with `reply_to_message` EMPTY, the parent
  described in `external_reply` instead. Reading only `reply_to_message` would
  have fixed a case that was never the problem. The external parent's
  attachment types are carried on their own `domain.Message` fields
  (`ExternalReplyMediaKinds`, `ExternalReplyDocumentExtensions`,
  `ExternalReplyDocumentMIMETypes`) and rendered by the same function as the
  in-chat one, so the two can never describe the same `.apk` differently.
  Exactly one parent line is emitted, whichever way the parent arrived.
- The external parent is deliberately NOT reconstructed into `ReplyTo`, even
  though that would have been the shorter patch. `ReplyTo` is what a
  moderator's `/spam` and `/ham` act on — it names the message to delete and
  the author to ban — and an external reply points at a message id in a chat
  the bot does not moderate. A synthetic parent there would silently re-aim an
  admin command outside the chat.
- Hard rules deliberately still ignore the reply parent, in-chat or external:
  replying to an `.apk` is also what someone warning "не ставьте, это вирус"
  does, so an automatic `delete_mute` on such a reply would be a false ban. The
  reply context reaches only the advisory, fail-open LLM stage.
- Documentation corrected where it contradicted the code. `README.md` no longer
  claims that a failed evidence copy always means "nothing applied";
  `AGENTS.md` and the `admin.Commands` comment no longer promise the same
  fail-closed behaviour for a human and for a detector, which the manual
  exception directly below them already denied; `docs/architecture.md` no
  longer denies that any part of a reply reaches `detect.Normalize` (the
  quote the REPLIER attached does, and always did), and its
  "current implementation boundaries" no longer describe the admin buttons as
  acknowledging without unmuting, unbanning, deleting evidence or training —
  they have done all four for some time. The deliberate narrowness of the
  extension and MIME patterns (ASCII, 12 characters, 64 per MIME component
  against RFC 6838's 127) is now stated where the contract is described, so it
  reads as the privacy trade it is rather than as a bug.

## [0.15.0] - 2026-08-27

### Added

- Hard rules can now block Telegram documents by normalized extension and/or
  MIME type (`detection.rules.banned_document_extensions` and
  `banned_document_mime_types`). This closes the executable-attachment gap
  where an `.apk` looked to every detector like an ordinary `document` with a
  harmless caption. Only the extension and MIME type enter detection, audit
  details, and optional LLM metadata; the user-controlled filename is discarded.

### Changed

- The built-in LLM prompt now explicitly includes unsolicited loan/credit
  offers and vague or euphemistic job/earning offers in its spam definition.

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
