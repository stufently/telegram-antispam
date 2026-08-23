package domain

// Sender identifies who sent a message, already classified (see spec §4).
type Sender struct {
	Kind         SenderKind
	UserID       int64
	SenderChatID int64
	Username     string
	DisplayName  string
}

// Entity is a normalized Telegram message entity (from Entities or
// CaptionEntities), used by detectors to inspect links, mentions, and other
// marked-up spans without depending on library types.
type Entity struct {
	Type   string
	URL    string
	Offset int
	Length int
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
	Entities           []Entity
	SenderTag          string
	ExternalReplyText  string
	PollOptionTexts    []string
	EditDate           int64
	// MediaKinds lists the attachment types this message carries, by their
	// Bot API field names ("photo", "video", "voice", ...). It replaces the
	// earlier single HasMedia flag, which told a detector that SOMETHING was
	// attached but never what — and so could not tell a sticker reply apart
	// from a captionless promo image, the one shape that reaches no text
	// detector at all. Empty means no attachment.
	MediaKinds []string
	// Forwarded is true when the message carries a forward_origin, i.e. it
	// was forwarded from somewhere rather than typed here. It is a SIGNAL,
	// not a verdict: forwarding is ordinary chat behavior, and the useful
	// reading of it ("a newcomer whose first act is forwarding a channel
	// post") only exists in combination with the other stages.
	Forwarded bool
	// ForwardedFromChat narrows Forwarded to origins that are a channel or
	// a group, as opposed to a person. A relayed channel post is the shape
	// casino/investment spam actually takes; a forwarded message from a
	// friend is not.
	ForwardedFromChat bool
	// HasKeyboard is true when an inline keyboard is attached to the
	// message. Only bots can attach one, so in a group where ordinary
	// members are humans it says the message came through a bot.
	HasKeyboard bool
	// ViaBot is true when the message was produced through an inline bot
	// (Telegram's via_bot). It is the innocent explanation for HasKeyboard
	// and for an otherwise odd-looking media message, so the two travel
	// together or neither is worth reading.
	ViaBot bool
	// ServiceKind names a Telegram service message ("join", "leave") and is
	// empty for an ordinary one. Such messages have no author to moderate;
	// the only thing to decide about them is whether to leave them in the
	// chat.
	ServiceKind string
	// ReplyTo is the message this one replies to, one level deep and never
	// recursive. Detection ignores it; moderator commands need it, because
	// "/spam" as a reply is the only way a human can point at a message the
	// bot already let through, and the Bot API offers no way to fetch a
	// message by id afterwards — if the update does not carry it, it is
	// gone.
	ReplyTo *Message
}

// HasMedia reports whether the message carries any attachment. It replaces
// the former field of the same name so the two can never disagree.
func (m Message) HasMedia() bool { return len(m.MediaKinds) > 0 }

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
	// ReviewOnly marks a verdict that must reach a human but must NOT be
	// enforced: the evidence is copied to the admin chat with its buttons
	// and the sanction is skipped, whatever the chat's mode says.
	//
	// It exists for signals that are suggestive but not probative — a
	// newcomer's captionless photo is the first one. Enforcing those
	// automatically bans real people for posting a picture; ignoring them
	// leaves the one message shape no text detector can see completely
	// unwatched. A verdict is therefore actionable (an incident is raised)
	// while carrying its own veto over the action.
	ReviewOnly bool
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
	// Tokens is the normalized token list of the offending message, carried
	// so the incident machine can persist it for admin-feedback training.
	// It is not raw text: see store.SaveIncidentTokens for the privacy
	// rationale and retention.
	Tokens []string
}
