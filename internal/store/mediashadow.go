package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// SignalIncident is one incident whose audit verdict carries a given signal,
// with the moderators' decision on it. It holds ids, the signal's detail and
// the outcome — never message text.
type SignalIncident struct {
	ID        int64
	ChatID    int64
	MessageID int
	CreatedAt int64
	State     string
	DryRun    bool
	Decision  string
	Detail    string
}

// IncidentsWithSignal lists incidents created at or after since (unix
// seconds) whose audit verdict carries the named signal, oldest first. The
// LIKE only narrows the scan; the JSON is decoded and matched exactly, so a
// signal whose DETAIL happens to contain the name is not a match.
func (db *DB) IncidentsWithSignal(name string, since int64) ([]SignalIncident, error) {
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(name) + "%"
	rows, err := db.sql.Query(`
SELECT i.id, i.chat_id, i.message_id, i.created_at, i.state, i.dry_run, i.decision, a.signals
FROM incidents i JOIN audit a ON a.incident_id = i.id
WHERE i.created_at >= ? AND a.signals LIKE ? ESCAPE '\'
ORDER BY i.id`, since, pattern)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SignalIncident
	seen := map[int64]bool{}
	for rows.Next() {
		var r SignalIncident
		var dry int
		var raw string
		if err := rows.Scan(&r.ID, &r.ChatID, &r.MessageID, &r.CreatedAt, &r.State, &dry, &r.Decision, &raw); err != nil {
			return nil, err
		}
		var signals []domain.Signal
		if err := json.Unmarshal([]byte(raw), &signals); err != nil {
			return nil, fmt.Errorf("incident %d: audit signals: %w", r.ID, err)
		}
		for _, s := range signals {
			if s.Name == name && !seen[r.ID] {
				seen[r.ID] = true
				r.DryRun = dry != 0
				r.Detail = s.Detail
				out = append(out, r)
				break
			}
		}
	}
	return out, rows.Err()
}
