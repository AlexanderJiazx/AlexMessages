/**
 * ChatStore reducer + action tests. A MockSocket lets us drive server events
 * synchronously and inspect client sends; API methods are stubbed per test.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../src/api";
import { ChatSocket, type SocketLike, type WebSocketFactory } from "../src/socket";
import { ChatStore, SEND_TIMEOUT_MS } from "../src/store";
import type { ServerEvent } from "../src/types";

// ---------- MockSocket: captures sent frames, exposes manual triggers ----------

class MockSocket implements SocketLike {
  sent: string[] = [];
  closed = false;
  private listeners = new Map<string, ((ev: { data?: string; code?: number }) => void)[]>();

  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.closed = true;
    this.fire("close", { code: 1000 });
  }
  addEventListener(type: string, listener: (ev: { data?: string; code?: number }) => void): void {
    const arr = this.listeners.get(type) || [];
    arr.push(listener);
    this.listeners.set(type, arr);
  }
  fire(type: "open" | "message" | "close" | "error", ev: { data?: string; code?: number } = {}): void {
    for (const l of this.listeners.get(type) || []) l(ev);
  }
  open(): void {
    this.fire("open");
  }
  event(data: ServerEvent): void {
    this.fire("message", { data: JSON.stringify(data) });
  }
}

function makeHarness() {
  const sockets: MockSocket[] = [];
  const factory: WebSocketFactory = () => {
    const s = new MockSocket();
    sockets.push(s);
    return s;
  };
  const api = new ApiClient({ baseUrl: "http://test" });
  const store = new ChatStore({
    api,
    wsUrl: () => "ws://test/ws",
    WebSocketImpl: factory,
  });
  return { api, store, sockets };
}

const me = { id: 1, username: "alice", display_name: "Alice", bio: "", avatar: "" };
const bob = { id: 2, username: "bob", display_name: "Bob", bio: "hi", avatar: "" };

function initFrame(overrides: Partial<Extract<ServerEvent, { type: "init" }>> = {}): ServerEvent {
  return {
    type: "init",
    me,
    users: [me, bob],
    contacts: [],
    online: [2],
    dm_threads: [{ channel: "dm:1:2", peer_id: 2 }],
    dm_state: {
      "dm:1:2": {
        pinned: false,
        last_read_at: 100,
        cleared_at: 0,
        force_unread: false,
        unread_count: 0,
        peer_last_read_at: 50,
      },
    },
    history: { "dm:1:2": [] },
    history_has_more: { "dm:1:2": false },
    page_size: 50,
    max_upload: 20 * 1024 * 1024,
    ...overrides,
  };
}

function connectAndInit(store: ChatStore, sockets: MockSocket[], init?: ServerEvent) {
  store.connect();
  sockets[0].open();
  sockets[0].event(init ?? initFrame());
}

describe("ChatStore init", () => {
  beforeEach(() => vi.useRealTimers());

  it("applies the init frame and restores state", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    const s = store.state;
    expect(s.me?.username).toBe("alice");
    expect(s.users[2]?.display_name).toBe("Bob");
    expect(s.online.has(2)).toBe(true);
    expect(s.dmThreads).toHaveLength(1);
    expect(store.dmStateFor("dm:1:2").peerLastReadAt).toBe(50);
    expect(s.ready).toBe(true);
  });

  it("activates the saved channel when it exists", () => {
    const { store, sockets } = makeHarness();
    store["opts"] = { ...store["opts"], storage: memStorage({ am_last_channel: "dm:1:2" }) };
    connectAndInit(store, sockets);
    expect(store.state.activeChannel).toBe("dm:1:2");
    // switching to a dm sends the "switch" frame
    expect(sockets[0].sent.map((f) => JSON.parse(f).type)).toContain("switch");
  });
});

describe("optimistic send", () => {
  it("appends a sending bubble, then reconciles on echo", () => {
    vi.useFakeTimers();
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    const res = store.sendMessage("hello bob");
    expect(res).toBeTruthy();
    const arr = store.state.history["dm:1:2"];
    expect(arr).toHaveLength(1);
    expect(arr[0]._status).toBe("sending");
    expect(store.deliveryStatus("dm:1:2")?.label).toBe("Sending…");

    const cid = arr[0].client_id!;
    sockets[0].event({
      type: "message",
      channel: "dm:1:2",
      message: {
        id: "srv1",
        type: "message",
        channel: "dm:1:2",
        user_id: 1,
        author: me,
        text: "hello bob",
        reply_to: null,
        attachments: [],
        created_at: 200,
        edited_at: null,
        client_id: cid,
      },
    });
    expect(arr).toHaveLength(1); // no duplicate
    expect(arr[0].id).toBe("srv1");
    expect(arr[0]._status).toBe("delivered");
    expect(store.deliveryStatus("dm:1:2")?.label).toBe("Delivered");
    vi.useRealTimers();
  });

  it("flags a send failed after the timeout, retry re-sends", () => {
    vi.useFakeTimers();
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    store.sendMessage("will fail");
    vi.advanceTimersByTime(SEND_TIMEOUT_MS + 10);
    const m = store.state.history["dm:1:2"][0];
    expect(m._status).toBe("failed");
    const before = sockets[0].sent.length;
    store.retrySend(m.client_id!);
    expect(m._status).toBe("sending");
    const last = JSON.parse(sockets[0].sent[sockets[0].sent.length - 1]);
    expect(last.client_id).toBe(m.client_id);
    expect(sockets[0].sent.length).toBe(before + 1);
    vi.useRealTimers();
  });
});

describe("incoming messages", () => {
  it("increments unread when the channel is inactive", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    store.switchChannel(null);
    sockets[0].event({
      type: "message",
      channel: "dm:1:2",
      message: mkMsg("m1", 2, "hi alice"),
    });
    expect(store.dmStateFor("dm:1:2").unreadCount).toBe(1);
  });

  it("marks read instead of counting when the channel is open and foregrounded", () => {
    const { api, store, sockets } = makeHarness();
    const read = vi.spyOn(api, "markDMRead").mockResolvedValue({ ok: true, last_read_at: 500 });
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    sockets[0].event({
      type: "message",
      channel: "dm:1:2",
      message: mkMsg("m2", 2, "you there?"),
    });
    expect(store.dmStateFor("dm:1:2").unreadCount).toBe(0);
    expect(read).toHaveBeenCalledWith("dm:1:2");
  });

  it("fires onNotify for backgrounded incoming messages", () => {
    const { store, sockets } = makeHarness();
    const seen: { channel: string }[] = [];
    (store as unknown as { opts: { onNotify: (n: { channel: string }) => void } }).opts.onNotify = (
      n,
    ) => seen.push(n);
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    store.setForeground(false);
    sockets[0].event({ type: "message", channel: "dm:1:2", message: mkMsg("m3", 2, "ping") });
    expect(seen).toHaveLength(1);
    expect(seen[0].channel).toBe("dm:1:2");
  });
});

describe("edits, receipts, misc events", () => {
  it("message_edited rewrites text and stamps edited_at", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets, initFrame({
      history: { "dm:1:2": [mkMsg("x1", 2, "original")] },
    }));
    sockets[0].event({ type: "message_edited", channel: "dm:1:2", id: "x1", text: "edited", edited_at: 999 });
    const m = store.state.history["dm:1:2"][0];
    expect(m.text).toBe("edited");
    expect(m.edited_at).toBe(999);
  });

  it("dm_read from the peer flips our delivery remark to Read", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    store.sendMessage("see this?");
    const local = store.state.history["dm:1:2"][0];
    local.id = "srv9";
    local._status = "delivered";
    local.created_at = 300;
    sockets[0].event({ type: "dm_read", channel: "dm:1:2", user_id: 2, last_read_at: 400 });
    expect(store.deliveryStatus("dm:1:2")?.label).toBe("Read");
  });

  it("dm_read from ourselves clears unread (another tab)", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets, initFrame({
      dm_state: {
        "dm:1:2": {
          pinned: false, last_read_at: 0, cleared_at: 0,
          force_unread: false, unread_count: 3, peer_last_read_at: 0,
        },
      },
    }));
    sockets[0].event({ type: "dm_read", channel: "dm:1:2", user_id: 1, last_read_at: 600 });
    expect(store.dmStateFor("dm:1:2").unreadCount).toBe(0);
    expect(store.dmStateFor("dm:1:2").lastReadAt).toBe(600);
  });

  it("profile_update merges known users and ignores strangers", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    sockets[0].event({ type: "profile_update", profile: { ...bob, display_name: "Bobby" } });
    expect(store.state.users[2].display_name).toBe("Bobby");
    sockets[0].event({ type: "profile_update", profile: { id: 99, username: "x", display_name: "X", bio: "", avatar: "" } });
    expect(store.state.users[99]).toBeUndefined();
  });
});

describe("thread actions", () => {
  it("openDM pushes a thread, sends open_dm, and switches", () => {
    const { store, sockets } = makeHarness();
    connectAndInit(store, sockets);
    store.state.users[3] = { id: 3, username: "c", display_name: "C", bio: "", avatar: "" };
    store.openDM(3);
    expect(store.state.activeChannel).toBe("dm:1:3");
    expect(sockets[0].sent.map((f) => JSON.parse(f).type)).toContain("open_dm");
    expect(store.state.dmThreads.find((t) => t.channel === "dm:1:3")).toBeTruthy();
  });

  it("deleteDm clears local state and detaches the thread", async () => {
    const { api, store, sockets } = makeHarness();
    vi.spyOn(api, "deleteDM").mockResolvedValue({ ok: true, cleared_at: 700 });
    connectAndInit(store, sockets);
    store.switchChannel("dm:1:2");
    await store.deleteDm("dm:1:2");
    expect(store.state.dmThreads.find((t) => t.channel === "dm:1:2")).toBeUndefined();
    expect(store.state.activeChannel).toBeNull();
    expect(store.state.history["dm:1:2"]).toHaveLength(0);
  });

  it("markUnread sets a forced unread count", async () => {
    const { api, store, sockets } = makeHarness();
    vi.spyOn(api, "markDMUnread").mockResolvedValue({ ok: true, last_read_at: 0 });
    connectAndInit(store, sockets, initFrame({
      history: { "dm:1:2": [mkMsg("a", 2, "one"), mkMsg("b", 1, "two"), mkMsg("c", 2, "three")] },
    }));
    await store.markUnread("dm:1:2");
    // 2 messages from the peer → unreadCount 2
    expect(store.dmStateFor("dm:1:2").unreadCount).toBe(2);
  });
});

describe("history paging", () => {
  it("loadOlder prepends and dedupes", async () => {
    const { api, store, sockets } = makeHarness();
    connectAndInit(store, sockets, initFrame({
      history: { "dm:1:2": [mkMsg("m5", 2, "newest", 500)] },
      history_has_more: { "dm:1:2": true },
    }));
    vi.spyOn(api, "history").mockResolvedValue({
      channel: "dm:1:2",
      messages: [mkMsg("m3", 2, "older", 300), mkMsg("m4", 1, "mid", 400)],
      has_more: true,
    });
    await store.loadOlder("dm:1:2");
    const arr = store.state.history["dm:1:2"];
    expect(arr.map((m) => m.id)).toEqual(["m3", "m4", "m5"]);
    expect(store.state.historyHasMore["dm:1:2"]).toBe(true);
  });
});

// ---------- helpers ----------

function mkMsg(id: string, uid: number, text: string, ts = 1000): Extract<ServerEvent, { type: "message" }>["message"] {
  return {
    id,
    type: "message",
    channel: "dm:1:2",
    user_id: uid,
    author: uid === 1 ? me : bob,
    text,
    reply_to: null,
    attachments: [],
    created_at: ts,
    edited_at: null,
  };
}

function memStorage(seed: Record<string, string> = {}) {
  const map = new Map(Object.entries(seed));
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  };
}
