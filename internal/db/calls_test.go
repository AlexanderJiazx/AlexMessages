package db

import (
	"fmt"
	"slices"
	"testing"
)

// fmtID prints a nullable id for comparison: "<nil>" or the number.
func fmtID(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprint(*p)
}

// The admin panel is another process, so it can delete a user while a call
// they are on is live: the call is logged after the user row is gone. The
// deleted participant is stored as NULL, and the record InsertCallMessage
// returns (which is what gets broadcast) matches what was stored.
func TestInsertCallMessageAfterParticipantDeleted(t *testing.T) {
	initTestDB(t)
	alice := mustCreateUser(t, "alice")
	bob := mustCreateUser(t, "bob")
	ch := DMChannelID(alice, bob)
	answered := int64(100)

	check := func(rec HistoryMessage, author, caller, callee string) {
		t.Helper()
		got := []string{fmtID(rec.UserID), fmtID(rec.Call.CallerID), fmtID(rec.Call.CalleeID)}
		if want := []string{author, caller, callee}; !slices.Equal(got, want) {
			t.Fatalf("%s returned author/caller/callee = %v, want %v", rec.ID, got, want)
		}
		msgs, err := FetchChannelWindow(ch, 50, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if m.ID != rec.ID {
				continue
			}
			if m.Call == nil {
				t.Fatalf("%s was stored without its calls row", rec.ID)
			}
			stored := []string{fmtID(m.UserID), fmtID(m.Call.CallerID), fmtID(m.Call.CalleeID)}
			if !slices.Equal(stored, got) || m.Call.ID != rec.Call.ID {
				t.Fatalf("%s stored %v (call %d), returned %v (call %d)", rec.ID, stored, m.Call.ID, got, rec.Call.ID)
			}
			return
		}
		t.Fatalf("%s was not stored", rec.ID)
	}

	rec, err := InsertCallMessage("m1", ch, alice, bob, "video", CallCompleted, 90, &answered, 130)
	if err != nil {
		t.Fatal(err)
	}
	check(rec, fmt.Sprint(alice), fmt.Sprint(alice), fmt.Sprint(bob))

	// The callee is gone.
	if err := DeleteUser(bob); err != nil {
		t.Fatal(err)
	}
	rec, err = InsertCallMessage("m2", ch, alice, bob, "audio", CallDeclined, 140, nil, 150)
	if err != nil {
		t.Fatalf("callee deleted: %v", err)
	}
	check(rec, fmt.Sprint(alice), fmt.Sprint(alice), "<nil>")

	// The caller, who authors the thread entry, is gone too.
	if err := DeleteUser(alice); err != nil {
		t.Fatal(err)
	}
	rec, err = InsertCallMessage("m3", ch, alice, bob, "audio", CallNoAnswer, 160, nil, 170)
	if err != nil {
		t.Fatalf("caller deleted: %v", err)
	}
	check(rec, "<nil>", "<nil>", "<nil>")
	if rec.Text != "Missed voice call" || rec.Call.Status != CallNoAnswer {
		t.Fatalf("m3 = %q / %s", rec.Text, rec.Call.Status)
	}
}
