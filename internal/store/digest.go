package store

import "github.com/stufently/telegram-antispam/internal/domain"

// ActionCountsSince returns action -> count for audit rows with
// created_at >= ts (unix seconds), split by what actually became of each one.
//
// An audit row is written at the pending stage — before the dry-run gate and
// before any action is applied — so the row alone proves only that a verdict
// was reached. The truth about what happened lives on the incident: dry_run
// (recorded at insert and never updated) and state. Joining them here keeps
// one source of truth instead of copying the flag onto the audit row, and
// separates the three outcomes a caller must not conflate:
//
//   - applied:    live mode, and the incident reached an acted state;
//   - dryRun:     simulated, nothing was carried out;
//   - incomplete: live mode, but the incident never got as far as acting
//     (evidence copy or admin send failed, the sanction errored, or it is
//     still in flight).
//
// Read-only.
func (db *DB) ActionCountsSince(ts int64) (applied, dryRun, incomplete map[string]int, err error) {
	rows, err := db.Read().Query(`
SELECT a.action, i.dry_run, i.state, COUNT(*)
FROM audit a
JOIN incidents i ON i.id = a.incident_id
WHERE a.created_at >= ?
GROUP BY a.action, i.dry_run, i.state`, ts)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	applied = make(map[string]int)
	dryRun = make(map[string]int)
	incomplete = make(map[string]int)
	for rows.Next() {
		var action, state string
		var dry, n int
		if err := rows.Scan(&action, &dry, &state, &n); err != nil {
			return nil, nil, nil, err
		}
		switch {
		case dry != 0:
			dryRun[action] += n
		case actedStates[domain.IncidentState(state)]:
			applied[action] += n
		default:
			incomplete[action] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return applied, dryRun, incomplete, nil
}

// actedStates are the incident states reached only after the sanction was
// actually applied (see incident.Machine.Handle's ordering).
var actedStates = map[domain.IncidentState]bool{
	domain.StateActed:   true,
	domain.StateCleaned: true,
	domain.StateDone:    true,
}
