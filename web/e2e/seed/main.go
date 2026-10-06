// Seed creates the E2E fixture database: approved users with a known
// password plus a little DM history between alice and bob, so the Playwright
// suite can exercise real conversations. Run it with the working directory
// set to the scratch dir that will host data/ (see e2e/start-server.sh).
package main

import (
	"fmt"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

const password = "e2e-password-123"

func main() {
	if err := db.InitDB(); err != nil {
		panic(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		panic(err)
	}
	type U struct {
		name  string
		disp  string
		admin bool
	}
	ids := map[string]int{}
	for _, u := range []U{
		{"e2e_alice", "Alice", false},
		{"e2e_bob", "Bob", false},
		{"e2e_carol", "Carol", false},
		{"e2e_admin", "Admin", true},
	} {
		existing, _ := db.GetUserByUsername(u.name)
		if existing != nil {
			ids[u.name] = existing.ID
			continue
		}
		id, err := db.CreateUser(u.name, hash, u.disp, "approved", u.admin)
		if err != nil {
			panic(fmt.Sprintf("create %s: %v", u.name, err))
		}
		ids[u.name] = id
	}

	// Mutual contacts so the threads show in the rail for both sides.
	for _, pair := range [][2]string{
		{"e2e_alice", "e2e_bob"},
		{"e2e_bob", "e2e_alice"},
		{"e2e_alice", "e2e_carol"},
		{"e2e_carol", "e2e_alice"},
	} {
		if err := db.AddContact(ids[pair[0]], ids[pair[1]]); err != nil {
			panic(err)
		}
	}

	// Normalize dm_state so every suite run starts from a clean slate:
	// unpinned, read, nothing force-unread.
	for _, pair := range [][2]string{
		{"e2e_alice", "e2e_bob"},
		{"e2e_bob", "e2e_alice"},
		{"e2e_alice", "e2e_carol"},
		{"e2e_carol", "e2e_alice"},
		{"e2e_bob", "e2e_carol"},
		{"e2e_carol", "e2e_bob"},
	} {
		ch := db.DMChannelID(ids[pair[0]], ids[pair[1]])
		if err := db.SetDMPinned(ids[pair[0]], ch, false); err != nil {
			panic(err)
		}
		if err := db.SetDMLastRead(ids[pair[0]], ch, db.NowTS()); err != nil {
			panic(err)
		}
	}

	// A little back-and-forth history so the stream has content to render.
	ch := db.DMChannelID(ids["e2e_alice"], ids["e2e_bob"])
	if msgs, _ := db.FetchChannelWindow(ch, 5, nil, nil); len(msgs) == 0 {
		alice, bob := ids["e2e_alice"], ids["e2e_bob"]
		if _, err := db.InsertMessage("seed0000000a", ch, &bob,
			"Hi Alice — check https://example.com when you can.", nil, "text", nil); err != nil {
			panic(err)
		}
		ts, err := db.InsertMessage("seed0000000b", ch, &alice, "Will do, thanks!", nil, "text", nil)
		if err != nil {
			panic(err)
		}
		if err := db.UpdateMessageText("seed0000000b", "Will do — edited!", ts); err != nil {
			panic(err)
		}
	}
	fmt.Println("seeded users:", ids)
}
