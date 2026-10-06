import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChatSocket, type SocketLike } from "../src/socket";

class FakeSocket implements SocketLike {
  sent: string[] = [];
  private listeners = new Map<string, ((ev: { data?: string; code?: number }) => void)[]>();
  send(d: string) {
    this.sent.push(d);
  }
  close() {
    this.fire("close", { code: 1000 });
  }
  addEventListener(t: string, l: (ev: { data?: string; code?: number }) => void) {
    const a = this.listeners.get(t) || [];
    a.push(l);
    this.listeners.set(t, a);
  }
  fire(t: "open" | "message" | "close" | "error", ev: { data?: string; code?: number } = {}) {
    for (const l of this.listeners.get(t) || []) l(ev);
  }
}

describe("ChatSocket", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("queues sends while closed and flushes on open", () => {
    const made: FakeSocket[] = [];
    const sock = new ChatSocket({
      url: "ws://x/ws",
      WebSocketImpl: () => {
        const s = new FakeSocket();
        made.push(s);
        return s;
      },
      onEvent: () => {},
      pingIntervalMs: 0,
    });
    sock.connect();
    sock.send({ type: "ping" });
    expect(made[0].sent).toHaveLength(0);
    made[0].fire("open");
    expect(made[0].sent.map((f) => JSON.parse(f).type)).toEqual(["ping"]);
    sock.destroy();
  });

  it("appends the bearer token to the URL", () => {
    let url = "";
    new ChatSocket({
      url: "ws://x/ws",
      token: "abc def",
      WebSocketImpl: (u) => {
        url = u;
        return new FakeSocket();
      },
      onEvent: () => {},
      pingIntervalMs: 0,
    }).connect();
    expect(url).toBe("ws://x/ws?token=abc%20def");
  });

  it("reconnects after a drop and reports down-status after grace", () => {
    const made: FakeSocket[] = [];
    const statuses: string[] = [];
    const sock = new ChatSocket({
      url: "ws://x/ws",
      WebSocketImpl: () => {
        const s = new FakeSocket();
        made.push(s);
        return s;
      },
      onEvent: () => {},
      onStatus: (s) => statuses.push(s),
      pingIntervalMs: 0,
      downGraceMs: 3000,
      retryMs: 1500,
    });
    sock.connect();
    made[0].fire("open");
    made[0].fire("close", { code: 1006 });
    vi.advanceTimersByTime(3200);
    expect(statuses).toContain("closed");
    vi.advanceTimersByTime(1500);
    expect(made).toHaveLength(2); // reconnected
    sock.destroy();
  });

  it("reports 4401 as closed and never retries", () => {
    const made: FakeSocket[] = [];
    const statuses: { s: string; code?: number }[] = [];
    const sock = new ChatSocket({
      url: "ws://x/ws",
      WebSocketImpl: () => {
        const s = new FakeSocket();
        made.push(s);
        return s;
      },
      onEvent: () => {},
      onStatus: (s, info) => statuses.push({ s, code: info?.code }),
      pingIntervalMs: 0,
    });
    sock.connect();
    made[0].fire("close", { code: 4401 });
    vi.advanceTimersByTime(10000);
    expect(made).toHaveLength(1);
    expect(statuses.some((x) => x.code === 4401)).toBe(true);
    sock.destroy();
  });
});
