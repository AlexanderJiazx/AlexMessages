package db

import (
	"fmt"
	"testing"
)

// A user keeps at most MaxPushSubscriptions endpoints: an 11th drops the
// oldest, and refreshing an old endpoint makes it the newest.
func TestPushSubscriptionsCappedPerUser(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	endpoint := func(i int) string { return fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", i) }
	for i := 0; i < MaxPushSubscriptions; i++ {
		if err := UpsertPushSubscription(a, endpoint(i), "k", "a", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Every row was written in the same second; refreshing endpoint 0 must
	// still keep it when the 11th arrives.
	if err := UpsertPushSubscription(a, endpoint(0), "k2", "a2", ""); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPushSubscription(a, endpoint(MaxPushSubscriptions), "k", "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPushSubscription(b, "https://web.push.apple.com/bob", "k", "a", ""); err != nil {
		t.Fatal(err)
	}
	subs, err := ListPushSubscriptions(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != MaxPushSubscriptions {
		t.Fatalf("alice has %d subscriptions, want %d", len(subs), MaxPushSubscriptions)
	}
	have := map[string]bool{}
	for _, s := range subs {
		have[s.Endpoint] = true
	}
	if !have[endpoint(0)] || !have[endpoint(MaxPushSubscriptions)] || have[endpoint(1)] {
		t.Fatalf("wrong rows kept: %v", have)
	}
	if bs, _ := ListPushSubscriptions(b); len(bs) != 1 {
		t.Fatalf("bob's subscriptions = %d, want 1", len(bs))
	}
}

// Unsubscribing removes only the caller's own row.
func TestRemoveUserPushSubscriptionOwnRowsOnly(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	if err := UpsertPushSubscription(b, "https://web.push.apple.com/bob", "k", "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := RemoveUserPushSubscription(a, "https://web.push.apple.com/bob"); err != nil {
		t.Fatal(err)
	}
	if subs, _ := ListPushSubscriptions(b); len(subs) != 1 {
		t.Fatal("another user's unsubscribe removed bob's subscription")
	}
	if err := RemoveUserPushSubscription(b, "https://web.push.apple.com/bob"); err != nil {
		t.Fatal(err)
	}
	if subs, _ := ListPushSubscriptions(b); len(subs) != 0 {
		t.Fatal("bob's own unsubscribe left the row")
	}
}

// Mark-unread keeps the reader's read receipt: the peer's
// peer_last_read_position stays where it was, and the reader's unread count
// is 1 through force_unread, not every message of the thread.
func TestSetDMUnreadKeepsPeerReadReceipt(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	for i := 0; i < 3; i++ {
		if _, err := InsertMessage(fmt.Sprintf("m%d", i), ch, &a, "hi", nil, "message", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetDMLastRead(b, ch, NowTS()); err != nil {
		t.Fatal(err)
	}
	read, _ := GetDMState(b, ch)
	if err := SetDMUnread(b, ch); err != nil {
		t.Fatal(err)
	}
	st, _ := GetDMState(b, ch)
	if !st.ForceUnread || st.LastReadPosition != read.LastReadPosition || st.LastReadAt != read.LastReadAt {
		t.Fatalf("after mark-unread: %+v, before: %+v", st, read)
	}
	peerView, err := DMSummaries(a)
	if err != nil {
		t.Fatal(err)
	}
	if peerView[ch].PeerLastReadPosition != read.LastReadPosition {
		t.Fatalf("alice sees bob's read position %d, want %d", peerView[ch].PeerLastReadPosition, read.LastReadPosition)
	}
	own, _ := DMSummaries(b)
	if own[ch].Unread != 0 {
		t.Fatalf("bob's counted unread = %d, want 0 (force_unread supplies the 1)", own[ch].Unread)
	}
	// Reading clears the flag.
	if _, err := SetDMReadThrough(b, ch, read.LastReadPosition); err != nil {
		t.Fatal(err)
	}
	if st, _ := GetDMState(b, ch); st.ForceUnread {
		t.Fatal("mark-read did not clear force_unread")
	}
}

// SetDMReadThrough reads up to the newest message at or below the given
// position, never past the channel's newest message, and never backwards.
func TestSetDMReadThroughPosition(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	for i := 0; i < 3; i++ {
		if _, err := InsertMessage(fmt.Sprintf("m%d", i), ch, &a, "hi", nil, "message", nil); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := FetchDMWindow(b, ch, 50, nil, "")
	if err != nil || len(msgs) != 3 {
		t.Fatalf("msgs=%d err=%v", len(msgs), err)
	}
	pos := map[string]int64{}
	for _, m := range msgs {
		pos[m.ID] = m.Position
	}
	st, err := SetDMReadThrough(b, ch, pos["m1"])
	if err != nil || st.LastReadPosition != pos["m1"] || st.LastReadAt == 0 {
		t.Fatalf("read through m1: %+v err=%v", st, err)
	}
	if n, _ := CountUnread(b, ch, 0, 0); n != 1 {
		t.Fatalf("unread after reading through m1 = %d, want 1", n)
	}
	// An older position does not move the read position back.
	if st, _ = SetDMReadThrough(b, ch, pos["m0"]); st.LastReadPosition != pos["m1"] {
		t.Fatalf("read position moved back to %d", st.LastReadPosition)
	}
	// A position beyond the channel's newest message stops at that message.
	if st, _ = SetDMReadThrough(b, ch, pos["m2"]+1000); st.LastReadPosition != pos["m2"] {
		t.Fatalf("read position = %d, want %d", st.LastReadPosition, pos["m2"])
	}
}
