package store

// ActionCountsSince returns a map of action -> count for audit rows with
// created_at >= ts (unix seconds). Read-only.
func (db *DB) ActionCountsSince(ts int64) (map[string]int, error) {
	rows, err := db.Read().Query(
		"SELECT action, COUNT(*) FROM audit WHERE created_at >= ? GROUP BY action", ts,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			return nil, err
		}
		counts[action] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}
