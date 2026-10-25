package census

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

const (
	clientA  = "4e1c7a52-0d35-4b39-9c55-0b3e1a2f6d10"
	embedded = "0f8b8f4e-1f6e-4d2a-9a51-3c8e2f0d9b11"
	failedA  = "11111111-2222-4333-8444-555555555555"
	failedB  = "66666666-7777-4888-8999-aaaaaaaaaaaa"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func yes() (bool, error) { return true, nil }
func no() (bool, error)  { return false, nil }

func report(client string) Report {
	return Report{ClientID: client, Platform: "android", Runtime: "rt1", EmbeddedUpdateID: embedded,
		CurrentUpdateID: embedded, Channel: "production", UserID: 7}
}

func TestRecordCreatesOnlyRegisteredInstalls(t *testing.T) {
	d := openTest(t)
	now := time.Unix(1_800_000_000, 0)
	res, err := d.Record(report(clientA), no, now)
	if err != nil || !res.Refused || res.Created {
		t.Fatalf("unregistered embedded id: %+v, %v", res, err)
	}
	if rows, _ := d.List(); len(rows) != 0 {
		t.Fatalf("rows %v", rows)
	}
	res, err = d.Record(report(clientA), yes, now)
	if err != nil || !res.Created {
		t.Fatalf("registered: %+v, %v", res, err)
	}
	// An existing row is updated without asking registered().
	r := report(clientA)
	r.CurrentUpdateID, r.UserID = failedB, 0
	res, err = d.Record(r, func() (bool, error) { t.Fatal("registered() called for an existing row"); return false, nil }, now.Add(time.Hour))
	if err != nil || res.Created || res.Refused {
		t.Fatalf("update: %+v, %v", res, err)
	}
	rows, err := d.List()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v, %v", rows, err)
	}
	got := rows[0]
	if got.CurrentUpdateID != failedB || got.UserID != nil || got.FirstSeen != now.Unix() || got.LastSeen != now.Add(time.Hour).Unix() {
		t.Fatalf("row %+v", got)
	}
}

func TestRecordReportsNewFailures(t *testing.T) {
	d := openTest(t)
	now := time.Unix(1_800_000_000, 0)
	r := report(clientA)
	r.FailedUpdateIDs = []string{failedA}
	res, _ := d.Record(r, yes, now)
	if len(res.NewFailedIDs) != 1 || res.NewFatalError {
		t.Fatalf("first report: %+v", res)
	}
	res, _ = d.Record(r, yes, now)
	if len(res.NewFailedIDs) != 0 {
		t.Fatalf("same failure reported again as new: %+v", res)
	}
	r.FailedUpdateIDs = []string{failedA, failedB}
	r.FatalError = "TypeError: undefined is not a function"
	res, _ = d.Record(r, yes, now)
	if len(res.NewFailedIDs) != 1 || res.NewFailedIDs[0] != failedB || !res.NewFatalError {
		t.Fatalf("new failure: %+v", res)
	}
	// The fatal error is sent once; later requests without it keep it.
	r.FatalError = ""
	res, _ = d.Record(r, yes, now.Add(time.Minute))
	rows, _ := d.List()
	if res.NewFatalError || rows[0].FatalError != "TypeError: undefined is not a function" || rows[0].FatalErrorAt == nil ||
		*rows[0].FatalErrorAt != now.Unix() || len(rows[0].FailedUpdateIDs) != 2 {
		t.Fatalf("row %+v", rows[0])
	}
}

func TestRecordCap(t *testing.T) {
	d := openTest(t)
	rowCap = 3
	t.Cleanup(func() { rowCap = MaxRows })
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 3; i++ {
		if res, err := d.Record(report(fmt.Sprintf("00000000-0000-4000-8000-%012d", i)), yes, now); err != nil || !res.Created {
			t.Fatalf("row %d: %+v %v", i, res, err)
		}
	}
	res, err := d.Record(report(clientA), yes, now)
	if err != nil || !res.Refused || !res.Full {
		t.Fatalf("over the cap: %+v, %v", res, err)
	}
	// A full table still updates existing rows.
	r := report("00000000-0000-4000-8000-000000000001")
	r.Channel = "beta"
	if res, err := d.Record(r, yes, now); err != nil || res.Refused {
		t.Fatalf("update when full: %+v, %v", res, err)
	}
	rows, _ := d.List()
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
}

func TestDeleteUserForUserAndPrune(t *testing.T) {
	d := openTest(t)
	now := time.Unix(1_800_000_000, 0)
	mine := report(clientA)
	other := report("00000000-0000-4000-8000-000000000001")
	other.UserID = 8
	stale := report("00000000-0000-4000-8000-000000000002")
	stale.UserID = 0
	d.Record(mine, yes, now)
	d.Record(other, yes, now)
	d.Record(stale, yes, now.Add(-PruneAfter-time.Second))

	rows, err := d.ForUser(7)
	if err != nil || len(rows) != 1 || rows[0].ClientID != clientA {
		t.Fatalf("ForUser: %v, %v", rows, err)
	}
	if n, err := d.Prune(now); err != nil || n != 1 {
		t.Fatalf("Prune: %d, %v", n, err)
	}
	if err := d.DeleteUser(7); err != nil {
		t.Fatal(err)
	}
	rows, _ = d.List()
	if len(rows) != 1 || rows[0].UserID == nil || *rows[0].UserID != 8 {
		t.Fatalf("left %v", rows)
	}
}

func TestSharedDoesNotCreateWhenAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if d, err := Shared(path, false); d != nil || err != nil {
		t.Fatalf("missing file: %v, %v", d, err)
	}
	d, err := Shared(path, true)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	if again, _ := Shared(path, false); again != d {
		t.Fatal("a second handle was opened")
	}
}
