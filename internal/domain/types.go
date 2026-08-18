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
