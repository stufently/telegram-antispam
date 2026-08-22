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
	HasMedia           bool
	// ReplyTo is the message this one replies to, one level deep and never
	// recursive. Detection ignores it; moderator commands need it, because
	// "/spam" as a reply is the only way a human can point at a message the
	// bot already let through, and the Bot API offers no way to fetch a
	// message by id afterwards — if the update does not carry it, it is
	// gone.
	ReplyTo *Message
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
	// Tokens is the normalized token list of the offending message, carried
	// so the incident machine can persist it for admin-feedback training.
	// It is not raw text: see store.SaveIncidentTokens for the privacy
	// rationale and retention.
	Tokens []string
}
