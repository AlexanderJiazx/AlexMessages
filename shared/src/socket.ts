/**
 * WebSocket control-plane client for Alex Messages.
 *
 * One socket carries every real-time event. Sends made while the socket is
 * down are queued and flushed on reconnect (matching the legacy web client).
 * Reconnects run on a fixed 1.5 s retry with a delayed "reconnecting"
 * notification so a brief blip never flashes UI.
 *
 * Liveness: the client pings every `pingIntervalMs` and the server answers
 * each ping with a pong frame. A socket that delivers no frame at all for
 * two ping intervals (a half-open TCP connection after a network change or
 * sleep, which the OS may take minutes to notice) is abandoned and replaced,
 * as is a connection attempt that has not opened after `connectTimeoutMs`.
 */

import type { ClientMessage, ServerEvent } from "./types";

export type SocketStatus = "connecting" | "open" | "closed";

export interface ChatSocketOptions {
  /**
   * Explicit ws(s):// URL. When `token` is provided it's appended as
   * `?token=` so native clients authenticate without cookies.
   */
  url: string;
  token?: string | null;
  /** Called for every decoded server event. */
  onEvent: (event: ServerEvent) => void;
  /** Socket lifecycle callback; `4401` reports as {code: 4401}. */
  onStatus?: (status: SocketStatus, info?: { code?: number }) => void;
  /** Injectable WebSocket implementation (tests / custom transports). */
  WebSocketImpl?: WebSocketFactory;
  /** Keepalive ping interval; 0 disables. Default 25 s. */
  pingIntervalMs?: number;
  /** Delay before reporting a dropped socket (hides sub-second blips). */
  downGraceMs?: number;
  /** Delay between reconnect attempts. */
  retryMs?: number;
  /** How long a connection attempt may take before it is abandoned. */
  connectTimeoutMs?: number;
}

/** Minimal structural type so non-DOM WebSocket impls can be injected. */
export interface SocketLike {
  send(data: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(
    type: "open" | "message" | "close" | "error",
    listener: (ev: { data?: string; code?: number }) => void,
  ): void;
}

export type WebSocketFactory = (url: string) => SocketLike;

const defaultFactory: WebSocketFactory = (url) => {
  const ws = new WebSocket(url);
  return {
    send: (d) => ws.send(d),
    close: (c, r) => ws.close(c, r),
    addEventListener: (t, l) => {
      ws.addEventListener(t, (ev) => l(ev as unknown as { data?: string; code?: number }));
    },
  };
};

export class ChatSocket {
  private ws: SocketLike | null = null;
  private ready = false;
  private queue: ClientMessage[] = [];
  private destroyed = false;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private downTimer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private connectTimer: ReturnType<typeof setTimeout> | null = null;
  private wakeTimer: ReturnType<typeof setTimeout> | null = null;
  private lastFrameAt = 0;
  private status: SocketStatus = "connecting";

  constructor(private opts: ChatSocketOptions) {}

  private get factory(): WebSocketFactory {
    return this.opts.WebSocketImpl ?? defaultFactory;
  }

  private get downGrace(): number {
    return this.opts.downGraceMs ?? 3000;
  }

  private get retryDelay(): number {
    return this.opts.retryMs ?? 1500;
  }

  private get pingMs(): number {
    return this.opts.pingIntervalMs ?? 25000;
  }

  private buildUrl(): string {
    let url = this.opts.url;
    if (this.opts.token) {
      url += (url.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(this.opts.token);
    }
    return url;
  }

  private setStatus(status: SocketStatus, code?: number): void {
    this.status = status;
    this.opts.onStatus?.(status, code != null ? { code } : undefined);
  }

  connect(): void {
    if (this.destroyed || this.ws) return;
    const ws = this.factory(this.buildUrl());
    this.ws = ws;
    // Events of a connection that was replaced or destroyed are ignored.
    const current = () => this.ws === ws && !this.destroyed;
    const timeout = this.opts.connectTimeoutMs ?? 10000;
    if (timeout > 0) {
      this.connectTimer = setTimeout(() => {
        this.connectTimer = null;
        if (current() && !this.ready) this.abandon(ws);
      }, timeout);
    }

    ws.addEventListener("open", () => {
      if (!current()) return;
      this.clearConnectTimer();
      this.ready = true;
      this.lastFrameAt = Date.now();
      this.cancelDown();
      this.setStatus("open");
      const queued = this.queue.splice(0);
      for (const p of queued) ws.send(JSON.stringify(p));
      if (this.pingMs > 0) {
        this.pingTimer = setInterval(() => {
          if (Date.now() - this.lastFrameAt > 2 * this.pingMs) this.abandon(ws);
          else this.send({ type: "ping" });
        }, this.pingMs);
      }
    });

    ws.addEventListener("message", (e) => {
      if (!current()) return;
      this.lastFrameAt = Date.now();
      let data: ServerEvent;
      try {
        data = JSON.parse(e.data as string);
      } catch {
        return;
      }
      this.opts.onEvent(data);
    });

    ws.addEventListener("close", (e) => {
      if (!current()) return;
      this.closed(e.code);
    });

    ws.addEventListener("error", () => {
      /* the close event follows with the real code */
    });
  }

  /** Handles the end of the current connection: schedules the reconnect. */
  private closed(code?: number): void {
    this.ws = null;
    this.ready = false;
    this.stopPing();
    this.clearConnectTimer();
    this.clearWake();
    if (code === 4401) {
      this.destroyed = true;
      this.setStatus("closed", 4401);
      return;
    }
    this.setStatus("connecting");
    if (!this.downTimer && this.downGrace > 0) {
      this.downTimer = setTimeout(() => {
        this.downTimer = null;
        if (!this.destroyed && !this.ready) this.opts.onStatus?.("closed", { code });
      }, this.downGrace);
    }
    this.scheduleRetry(this.retryDelay);
  }

  /** Gives up on a connection that stopped answering, and reconnects. */
  private abandon(ws: SocketLike): void {
    if (this.ws !== ws) return;
    this.ws = null; // detach first, so its own close event is ignored
    try {
      ws.close();
    } catch {
      /* already gone */
    }
    this.closed(1006);
  }

  private scheduleRetry(ms: number): void {
    if (this.retryTimer || this.destroyed) return;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.connect();
    }, ms);
  }

  /**
   * The app came back to the foreground: reconnect now if the socket is
   * down, or prove that the open socket still works (a ping that gets no
   * frame back within 5 s replaces the connection).
   */
  wake(): void {
    if (this.destroyed) return;
    if (!this.ws) {
      if (this.retryTimer) clearTimeout(this.retryTimer);
      this.retryTimer = null;
      this.connect();
      return;
    }
    if (!this.ready || this.wakeTimer) return;
    const ws = this.ws;
    const sentAt = Date.now();
    this.send({ type: "ping" });
    this.wakeTimer = setTimeout(() => {
      this.wakeTimer = null;
      if (this.ws === ws && this.lastFrameAt < sentAt) this.abandon(ws);
    }, 5000);
  }

  /** Queues a payload; flushes automatically once the socket opens. */
  send(payload: ClientMessage): void {
    if (this.ready && this.ws) {
      this.ws.send(JSON.stringify(payload));
    } else {
      this.queue.push(payload);
    }
  }

  private cancelDown(): void {
    if (this.downTimer) {
      clearTimeout(this.downTimer);
      this.downTimer = null;
    }
  }

  private clearConnectTimer(): void {
    if (this.connectTimer) {
      clearTimeout(this.connectTimer);
      this.connectTimer = null;
    }
  }

  private clearWake(): void {
    if (this.wakeTimer) {
      clearTimeout(this.wakeTimer);
      this.wakeTimer = null;
    }
  }

  private stopPing(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }

  /** Permanently shuts the socket down (no further reconnects). */
  destroy(): void {
    this.destroyed = true;
    this.ready = false;
    this.cancelDown();
    this.stopPing();
    this.clearConnectTimer();
    this.clearWake();
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
    const ws = this.ws;
    this.ws = null; // its close event is now ignored
    try {
      ws?.close();
    } catch {
      /* already gone */
    }
  }
}
