package telegram_test

// External test package (not `package telegram`) so this file can import
// both internal/telegram and internal/incident without creating the import
// cycle that package telegram itself avoids by declaring IncidentMachine
// locally (see bot.go). This is only a compile-time assertion that
// *incident.Machine keeps satisfying telegram.IncidentMachine.

import (
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

var _ telegram.IncidentMachine = (*incident.Machine)(nil)
