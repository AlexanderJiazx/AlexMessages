package db

import "testing"

func intp(v int) *int { return &v }

func seedDebug(t *testing.T) {
	t.Helper()
	rows := []DebugEvent{
		{TsMs: 1000, App: "messages", Level: "info", Username: "alice", Session: "tab-a", Event: "ws_open", Message: "connected"},
		{TsMs: 1001, App: "messages", Level: "error", Username: "alice", Session: "tab-a", Event: "ws_error", Message: "boom"},
		{TsMs: 1002, App: "meet", Level: "info", UserID: intp(7), Username: "bob", Session: "tab-b", Event: "join", Message: "Participant joined"},
		{TsMs: 1003, App: "meet", Level: "warn", Username: "guest", Session: "tab-c", Event: "reconnect_begin", Message: "retrying"},
	}
	for _, ev := range rows {
		if _, err := InsertDebugEvent(ev); err != nil {
			t.Fatalf("InsertDebugEvent: %v", err)
		}
	}
}

func TestDebugQueryFilters(t *testing.T) {
	initTestDB(t)
	seedDebug(t)

	all, err := QueryDebugEvents(DebugFilter{})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 events, got %d", len(all))
	}
	// Ascending id order (oldest first).
	if all[0].Event != "ws_open" || all[3].Event != "reconnect_begin" {
		t.Fatalf("unexpected order: %v .. %v", all[0].Event, all[3].Event)
	}

	cases := []struct {
		name string
		f    DebugFilter
		want int
	}{
		{"app", DebugFilter{App: "meet"}, 2},
		{"level", DebugFilter{Level: "info"}, 2},
		{"user-substring", DebugFilter{Username: "ali"}, 2},
		{"user-case-insensitive", DebugFilter{Username: "BOB"}, 1},
		{"session", DebugFilter{Session: "tab-b"}, 1},
		{"search-message", DebugFilter{Search: "retry"}, 1},
		{"search-event", DebugFilter{Search: "ws_"}, 2},
		{"combined", DebugFilter{App: "messages", Level: "error"}, 1},
		{"none-match", DebugFilter{App: "nope"}, 0},
	}
	for _, tc := range cases {
		got, err := QueryDebugEvents(tc.f)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, len(got))
		}
	}
}

func TestDebugSinceCursor(t *testing.T) {
	initTestDB(t)
	seedDebug(t)
	all, _ := QueryDebugEvents(DebugFilter{})
	mid := all[1].ID
	got, err := QueryDebugEvents(DebugFilter{SinceID: mid})
	if err != nil {
		t.Fatalf("since query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events after id %d, got %d", mid, len(got))
	}
	if got[0].ID <= mid {
		t.Fatalf("since cursor leaked an old row: %d <= %d", got[0].ID, mid)
	}
}

func TestDebugPruneAndLatest(t *testing.T) {
	initTestDB(t)
	for i := 0; i < 20; i++ {
		if _, err := InsertDebugEvent(DebugEvent{TsMs: int64(i), App: "messages", Level: "debug", Event: "tick"}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	latest, err := LatestDebugEventID()
	if err != nil || latest == 0 {
		t.Fatalf("LatestDebugEventID: id=%d err=%v", latest, err)
	}
	if err := PruneDebugEvents(5); err != nil {
		t.Fatalf("prune: %v", err)
	}
	remaining, _ := QueryDebugEvents(DebugFilter{Limit: 1000})
	if len(remaining) != 5 {
		t.Fatalf("after prune to 5, got %d rows", len(remaining))
	}
	// The newest rows must survive a prune.
	if remaining[len(remaining)-1].ID != latest {
		t.Fatalf("prune dropped the newest row: kept up to %d, latest %d", remaining[len(remaining)-1].ID, latest)
	}

	if err := ClearDebugEvents(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	empty, _ := QueryDebugEvents(DebugFilter{})
	if len(empty) != 0 {
		t.Fatalf("after clear, got %d rows", len(empty))
	}
}
