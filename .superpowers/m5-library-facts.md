# go-telegram/bot v1.23.0 — API surface facts for M5 planning

Source: `/home/deploy/go/pkg/mod/github.com/go-telegram/bot@v1.23.0/` (confirmed
via `go.mod:7` / `go.sum:5-6` — module cache is under `$GOPATH/pkg/mod`, NOT
under `.gopath/` in this worktree, despite that directory existing).

---

## 1. Reaction cleanup

**Three separate library methods exist** (all in `methods.go`), not just
`SetMessageReaction`:

- `func (b *Bot) SetMessageReaction(ctx context.Context, params *SetMessageReactionParams) (bool, error)`
  — `methods.go:211`. Calls `setMessageReaction`.
  Params (`methods_params.go:471-476`):
  ```go
  type SetMessageReactionParams struct {
      ChatID    any                   `json:"chat_id"`
      MessageID int                   `json:"message_id"`
      Reaction  []models.ReactionType `json:"reaction,omitempty"`
      IsBig     *bool                 `json:"is_big,omitempty"`
  }
  ```
  Passing `Reaction: nil` (or empty slice) clears **the bot's own** reaction
  on the message (per Bot API semantics — this call only ever affects the
  reaction set by the calling bot/user, not other users' reactions).

- `func (b *Bot) DeleteMessageReaction(ctx context.Context, params *DeleteMessageReactionParams) (bool, error)`
  — `methods.go:1249`. Calls `deleteMessageReaction`.
  Params (`methods_params.go:1472-1477`):
  ```go
  type DeleteMessageReactionParams struct {
      ChatID      any   `json:"chat_id"`
      MessageID   int   `json:"message_id"`
      UserID      int64 `json:"user_id,omitempty"`
      ActorChatID int64 `json:"actor_chat_id,omitempty"`
  }
  ```
  **This is the dedicated method for removing a specific user's (or actor
  chat's) reaction** — the Bot API 10.0 capability the task asked about.
  Set `UserID` (or `ActorChatID` for anonymous-admin reactions) to target
  someone other than the bot itself.

- `func (b *Bot) DeleteAllMessageReactions(ctx context.Context, params *DeleteAllMessageReactionsParams) (bool, error)`
  — `methods.go:1243`. Calls `deleteAllMessageReactions`.
  Params (`methods_params.go:1466-1470`):
  ```go
  type DeleteAllMessageReactionsParams struct {
      ChatID      any   `json:"chat_id"`
      UserID      int64 `json:"user_id,omitempty"`
      ActorChatID int64 `json:"actor_chat_id,omitempty"`
  }
  ```
  Note: no `MessageID` field — clears reactions across the chat for that
  user/actor (per param shape), not scoped to one message.

**Reaction-update delivery:** yes, fully modeled.
`models/reaction.go:83-92`:
```go
type MessageReactionUpdated struct {
    Chat        Chat           `json:"chat"`
    MessageID   int            `json:"message_id"`
    User        *User          `json:"user,omitempty"`
    ActorChat   *Chat          `json:"actor_chat,omitempty"`
    Date        int            `json:"date"`
    OldReaction []ReactionType `json:"old_reaction"`
    NewReaction []ReactionType `json:"new_reaction"`
}
```
and `models/reaction.go:100-106` for the aggregate-count variant:
```go
type MessageReactionCountUpdated struct {
    Chat      Chat            `json:"chat"`
    MessageID int             `json:"message_id"`
    Date      int             `json:"date"`
    Reactions []ReactionCount `json:"reactions"`
}
```
Exposed on `Update` (`models/update.go:14-15`):
```go
MessageReaction      *MessageReactionUpdated      `json:"message_reaction,omitempty"`
MessageReactionCount *MessageReactionCountUpdated `json:"message_reaction_count,omitempty"`
```
Field names: `Chat`/`chat`, `MessageID`/`message_id`, `User`/`user` (the
actor when it's a regular user), `ActorChat`/`actor_chat` (the actor when
it's an anonymous chat, e.g. an anonymous admin), `Date`/`date`,
`OldReaction`/`old_reaction`, `NewReaction`/`new_reaction` — both are
`[]ReactionType` (tagged union: emoji / custom_emoji / paid, see
`models/reaction.go:8-64`).

---

## 2. Ephemeral newcomer replies (Bot API 10.2)

**The library DOES expose this** — confirmed by grepping for "ephemeral"
across the module and finding a full, tested feature set (this is new
enough that it's covered by a dedicated `models/community_test.go`, i.e.
these are the 10.2 "community" additions).

Mechanism: `SendMessageParams` (and every other `SendXxxParams`, e.g.
`SendPhotoParams`, `SendStickerParams`, etc.) carries a `ReceiverUserID`
field:
```go
type SendMessageParams struct {
    BusinessConnectionID    string `json:"business_connection_id,omitempty"`
    ChatID                  any    `json:"chat_id"`
    ...
    ReceiverUserID          int64  `json:"receiver_user_id,omitempty"`   // methods_params.go:27
    ...
}
```
Setting `ChatID` to the group and `ReceiverUserID` to the target user's ID
sends a message visible **only to that user** in the group — this is
exactly Bot API 10.2 ephemeral messages, reached through the normal
`Send*` methods (no separate "SendEphemeral" call needed for the initial
send).

The response `Message` carries the ephemeral identity back
(`models/message.go:90-92`):
```go
SenderTag           string `json:"sender_tag,omitempty"`
ReceiverUser        *User  `json:"receiver_user,omitempty"`
EphemeralMessageID  int    `json:"ephemeral_message_id,omitempty"`
```
`EphemeralMessageID` is a **different id space from `message_id`** and is
required for the follow-up ephemeral-specific edit/delete calls, all
present in `methods.go:711-744` / `methods_params.go:900-946`:

- `EditEphemeralMessageText(ctx, *EditEphemeralMessageTextParams)`
- `EditEphemeralMessageMedia(ctx, *EditEphemeralMessageMediaParams)`
- `EditEphemeralMessageCaption(ctx, *EditEphemeralMessageCaptionParams)`
- `EditEphemeralMessageReplyMarkup(ctx, *EditEphemeralMessageReplyMarkupParams)`
- `DeleteEphemeralMessage(ctx, *DeleteEphemeralMessageParams)`

Every one of these params structs requires `ChatID`, `ReceiverUserID`, and
`EphemeralMessageID` (no `omitempty` on the latter two — they're mandatory
correlation keys), e.g.:
```go
type DeleteEphemeralMessageParams struct {
    ChatID             any   `json:"chat_id"`
    ReceiverUserID     int64 `json:"receiver_user_id"`
    EphemeralMessageID int   `json:"ephemeral_message_id"`
}
```
Test evidence confirming the wire shape and library behavior:
`methods_test.go:1427-1512` (`TestBot_EphemeralMessageMethods`, verifies
`receiver_user_id` + `ephemeral_message_id` are sent for all five edit/delete
calls) and `models/community_test.go:36-49` /
`models/community_test.go:76-90` (`TestMessage_Ephemeral`,
`TestReplyParameters_Ephemeral` — confirms `ReplyParameters.EphemeralMessageID`
exists too, at `models/reply.go:49`, for replying to an ephemeral message).

**Conclusion for item 2: no gap.** Ephemeral, per-user-visible replies in a
group are fully reachable via `SendMessageParams.ReceiverUserID` for the
initial send, plus the dedicated `EditEphemeralMessage*` /
`DeleteEphemeralMessage*` methods for follow-up mutation, all requiring the
returned `EphemeralMessageID`.

---

## 3. chat_member updates

`Update` fields (`models/update.go:26-27`):
```go
MyChatMember *ChatMemberUpdated `json:"my_chat_member,omitempty"`
ChatMember   *ChatMemberUpdated `json:"chat_member,omitempty"`
```
`ChatMemberUpdated` (`models/chat_member.go:9-18`):
```go
type ChatMemberUpdated struct {
    Chat                    Chat            `json:"chat"`
    From                    User            `json:"from"`
    Date                    int             `json:"date"`
    OldChatMember           ChatMember      `json:"old_chat_member"`
    NewChatMember           ChatMember      `json:"new_chat_member"`
    InviteLink              *ChatInviteLink `json:"invite_link,omitempty"`
    ViaJoinRequest          bool            `json:"via_join_request,omitempty"`
    ViaChatFolderInviteLink bool            `json:"via_chat_folder_invite_link,omitempty"`
}
```
`ChatMember` is a tagged union (`Type` + one of `Owner`/`Administrator`/
`Member`/`Restricted`/`Left`/`Banned` pointers), custom-(un)marshaled on
the `status` JSON field (`models/chat_member.go:31-104`).

**How a handler receives these:** the library's pattern-matching handler
registry (`RegisterHandler`, `RegisterHandlerRegexp`) only supports
`HandlerType` values `HandlerTypeMessageText`, `HandlerTypeCallbackQueryData`,
`HandlerTypeCallbackQueryGameShortName`, `HandlerTypePhotoCaption`
(`handlers.go:13-16`) — there is **no** handler-type for chat_member or
reaction updates. `findHandler` (`process_update.go:34-45`) iterates
registered handlers and, if none match, falls back to
`b.defaultHandlerFunc`. So **the only way to receive `Update.ChatMember`,
`Update.MyChatMember`, or `Update.MessageReaction` is via
`bot.WithDefaultHandler(...)`**, switching on the relevant `Update` field —
exactly the pattern this repo already uses in
`cmd/tg-antispam/main.go:184-223`.

**allowed_updates opt-in required:** confirmed in `get_updates.go:17-57`.
`getUpdatesParams.AllowedUpdates` is only populated
(`params.AllowedUpdates = b.allowedUpdates`, `get_updates.go:55-57`) if
`b.allowedUpdates != nil`; the field is `nil` by default and only set via
`bot.WithAllowedUpdates(params AllowedUpdates)` (`options.go:100-105`,
where `type AllowedUpdates []string` is defined in `get_updates.go:24`).
If never set, `getUpdates` omits `allowed_updates` entirely, which per
Telegram Bot API semantics delivers only the legacy default update types
— `chat_member` and `message_reaction` are **not** among those defaults
and must be explicitly requested. String constants for every allowed-update
value live at `models/update.go:34-62`
(`models.AllowedUpdateChatMember`, `models.AllowedUpdateMessageReaction`,
etc.) though this repo's `main.go` currently passes raw string literals
instead of these constants (see §5).

---

## 4. Sender/admin info for fake-admin detection

`func (b *Bot) GetChatAdministrators(ctx context.Context, params *GetChatAdministratorsParams) ([]models.ChatMember, error)`
— `methods.go:414`. Params: `GetChatAdministratorsParams{ ChatID any }`
(`methods_params.go:651`). Returns `[]models.ChatMember` — the same tagged
union described in §3.

Reading an admin's identity/title from the result:
- `models.ChatMemberAdministrator.User` (`models/chat_member.go:117`, plain
  `User` not `*User`) — gives `.FirstName`, `.Username`, etc.
- `models.ChatMemberAdministrator.CustomTitle` (`models/chat_member.go:136`,
  `json:"custom_title,omitempty"`) — the admin's custom title shown in the
  UI (what a spoofed "admin" name would be trying to imitate — this is the
  ground-truth title to diff a message's claimed signature against).
- `models.ChatMemberOwner.CustomTitle` also exists (`models/chat_member.go:111`)
  for the chat owner.

**`sender_tag` / author-signature concept on incoming messages:**
- `Message.AuthorSignature` (`models/message.go:110`,
  `json:"author_signature,omitempty"`) — the classic channel-post author
  signature field (only meaningful when `Chat.Type == "channel"` or the
  message came via a linked channel).
- `Message.SenderChat` (`models/message.go:87`, `*Chat`,
  `json:"sender_chat,omitempty"`) — set when the message was sent by an
  anonymous admin acting as the chat/channel rather than as a `User`; this
  is the field to check for "is this actually an anonymous-admin post" vs.
  a spoofed regular-member message.
- `Message.SenderTag` (`models/message.go:90`, `json:"sender_tag,omitempty"`,
  a NEW 10.2 field) — separate from `AuthorSignature`; also mirrored on
  `ChatMemberMember.Tag` and `ChatMemberRestricted.Tag`
  (`models/chat_member.go:144,169`, both `json:"tag,omitempty"`) and gated
  by `ChatMemberRestricted.CanEditTag` (`models/chat_member.go:166`). This
  looks like the new "chat tag" feature (a member-settable badge distinct
  from admin custom_title) — worth treating as untrusted/user-controlled
  text, not an admin-authority signal, since regular restricted/member
  users can apparently set it too (`CanEditTag` gates *editing* it, which
  implies members carry one).

For "fake admin" detection specifically: the ground truth is
`GetChatAdministrators` → `ChatMemberAdministrator.CustomTitle` /
`ChatMemberOwner.CustomTitle`, cross-checked against `Message.SenderChat`
(genuine anonymous-admin post) — `Message.SenderTag` should NOT be trusted
as an admin signal since it appears to be member-editable.

---

## 5. Existing port abstraction in this repo

`internal/telegram/port.go` — `Port` interface (lines 37-47):
```go
type Port interface {
    CopyMessages(ctx context.Context, dstChat, srcChat int64, ids []int) ([]int, error)
    DeleteMessages(ctx context.Context, chat int64, ids []int) error
    BanMember(ctx context.Context, chat, user int64) error
    RestrictMember(ctx context.Context, chat, user int64, perms Perms, until int64) error
    SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error)
    BanSenderChat(ctx context.Context, chat, senderChat int64) error
    GetChatAdministrators(ctx context.Context, chat int64) ([]Member, error)
    AnswerCallback(ctx context.Context, callbackID, text string) error
    EditAdminMarkup(ctx context.Context, adminChat int64, messageID int, buttons [][]Button) error
}
```
Supporting narrow types also in this file: `Perms{CanSend bool}`,
`Member{UserID, Status, Username, DisplayName}`,
`AdminMessage{Text, IncidentKey, SourceChatID, CopiedFromChatID,
CopyMessageIDs, Buttons}`, `Button{Text, Data}`.

`internal/telegram/livept.go` — `LivePort` implements `Port` against a real
`*bot.Bot` (`var _ Port = (*LivePort)(nil)`, line 27). Every method follows
one shape:
1. `submitSync[T]` or `submitSyncErr` (lines 83-112) wraps the call as a
   `queue.Job` submitted to `p.disp` (the rate-limit/priority/429-retry
   dispatcher), blocking until a terminal result arrives.
2. Inside the job closure: build the library's `*bot.XxxParams` struct,
   call `p.b.Xxx(ctx, params)`, and pass the error through `mapRetry(err)`
   (lines 59-76), which type-asserts `*bot.TooManyRequestsError` and turns
   a 429 into `queue.RetryAfter{Seconds: n}` so the dispatcher retries; any
   other error/nil passes through unchanged.
3. Library result types are flattened into the repo's narrow `Port` types
   before returning (e.g. `memberFromChatMember` / `memberFromUser`,
   lines 223-255, flatten the tagged-union `models.ChatMember` into
   `Member`).

**Pattern to follow for new methods** (e.g. `SetMessageReaction`,
`DeleteMessageReaction`): add the narrow method to the `Port` interface in
`port.go`, then in `livept.go` implement it as
`submitSyncErr(ctx, p.disp, chat, p.prio("MethodName"), func(ctx) error {
    _, err := p.b.LibraryMethod(ctx, &bot.LibraryMethodParams{...})
    return mapRetry(err)
})` (or `submitSync[T]` if a value must be returned, e.g. an
`EphemeralMessageID`).

`cmd/tg-antispam/main.go`:
- Handler registration is entirely through `tgbot.WithDefaultHandler`
  (line 184), no `RegisterHandler`/pattern handlers are used — the
  `switch` at lines 185-222 currently handles only `update.Message`,
  `update.EditedMessage`, and `update.CallbackQuery`. **`update.ChatMember`,
  `update.MyChatMember`, and `update.MessageReaction` are NOT yet switched
  on** — allowed, but the branches don't exist yet (relevant for M5 scope:
  the wiring exists to receive these updates, but nothing consumes them
  today).
- `allowed_updates` **is already configured** (line 180-183):
  ```go
  tgbot.WithAllowedUpdates([]string{
      "message", "edited_message", "callback_query",
      "chat_member", "my_chat_member", "message_reaction",
  }),
  ```
  So the app already opts into `chat_member`, `my_chat_member`, and
  `message_reaction` updates at the transport level — M5 work only needs
  to add the corresponding `switch` cases in the default handler and Port
  methods, not touch the `WithAllowedUpdates` call itself.
- `tgbot.WithNotAsyncHandlers()` + `tgbot.WithWorkers(1)` (lines 175-179)
  are pinned intentionally (see inline comments) for per-chat FIFO
  ordering, album dedup, and message-reaction dedup — any M5 reaction/
  chat-member handling must respect this single-inline-consumer
  assumption rather than spawning concurrent update processing.

---

## GAPS

**None found for the four capabilities investigated (reaction
delete/clear, ephemeral messages, chat_member delivery, admin
identity/custom_title).** The library (v1.23.0, tracking Bot API 10.2)
exposes all of them directly:

- Per-user reaction removal: `DeleteMessageReactionParams.UserID` (not just
  `SetMessageReaction` with an empty list, which only clears the bot's own
  reaction).
- Ephemeral messages: `SendMessageParams.ReceiverUserID` +
  `EditEphemeralMessage*`/`DeleteEphemeralMessage*` + `EphemeralMessageID`.
- `chat_member`/`my_chat_member`/`message_reaction` updates: delivered via
  `Update` fields, reachable only through `WithDefaultHandler` (no
  dedicated `RegisterHandler` type for them), and this repo's `main.go`
  already requests them in `allowed_updates`.
- Admin custom_title / anonymous-admin detection:
  `ChatMemberAdministrator.CustomTitle`, `ChatMemberOwner.CustomTitle`,
  `Message.SenderChat`.

The one soft caution flagged above (not a library gap, but a design trap):
`Message.SenderTag` / `ChatMember*.Tag` is a new 10.2 field that looks
superficially like an authority signal but is member-editable
(`ChatMemberRestricted.CanEditTag`) — do not use it as proof of admin
status; use `GetChatAdministrators` + `CustomTitle` for that instead.
