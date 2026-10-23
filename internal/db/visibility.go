package db

// VisibilityForUsers builds one view per connected user with two queries,
// regardless of the number of sockets or users. Duplicate tabs share a view.
func VisibilityForUsers(viewers []int) (map[int]map[int]struct{}, error) {
	out := map[int]map[int]struct{}{}
	for _, id := range viewers {
		out[id] = map[int]struct{}{id: {}}
	}
	rows, err := pool.Query("SELECT owner_id, contact_id FROM contacts")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var owner, peer int
		if err := rows.Scan(&owner, &peer); err != nil {
			rows.Close()
			return nil, err
		}
		if set := out[owner]; set != nil {
			set[peer] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = pool.Query("SELECT DISTINCT channel FROM messages WHERE channel LIKE 'dm:%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		a, b, ok := ParseDMChannel(ch)
		if !ok {
			continue
		}
		if set := out[a]; set != nil {
			set[b] = struct{}{}
		}
		if set := out[b]; set != nil {
			set[a] = struct{}{}
		}
	}
	return out, rows.Err()
}
