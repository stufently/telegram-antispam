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
