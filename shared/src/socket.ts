/**
 * WebSocket control-plane client for Alex Messages.
 *
 * One socket carries every real-time event. Sends made while the socket is
 * down are queued and flushed on reconnect (matching the legacy web client).
 * Reconnects run on a fixed 1.5 s retry with a delayed "reconnecting"
 * notification so a brief blip never flashes UI.
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
    if (this.destroyed) return;
    this.ws = this.factory(this.buildUrl());

    this.ws.addEventListener("open", () => {
      this.ready = true;
      this.cancelDown();
      this.setStatus("open");
      const queued = this.queue.splice(0);
      for (const p of queued) this.ws?.send(JSON.stringify(p));
      const pingMs = this.opts.pingIntervalMs ?? 25000;
      if (pingMs > 0) {
        this.pingTimer = setInterval(() => this.send({ type: "ping" }), pingMs);
      }
    });

    this.ws.addEventListener("message", (e) => {
      let data: ServerEvent;
      try {
        data = JSON.parse(e.data as string);
      } catch {
        return;
      }
      this.opts.onEvent(data);
    });

    this.ws.addEventListener("close", (e) => {
      this.ready = false;
      this.stopPing();
      if (e.code === 4401) {
        this.destroyed = true;
        this.setStatus("closed", 4401);
        return;
      }
      this.setStatus("connecting");
      if (!this.downTimer && this.downGrace > 0) {
        this.downTimer = setTimeout(() => {
          this.downTimer = null;
          this.opts.onStatus?.("closed", { code: e.code });
        }, this.downGrace);
      }
      if (!this.retryTimer && !this.destroyed) {
        this.retryTimer = setTimeout(() => {
          this.retryTimer = null;
          this.connect();
        }, this.retryDelay);
      }
    });

    this.ws.addEventListener("error", () => {
      /* the close event follows with the real code */
    });
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

  private stopPing(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }

  /** Permanently shuts the socket down (no further reconnects). */
  destroy(): void {
    this.destroyed = true;
    this.cancelDown();
    this.stopPing();
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
    try {
      this.ws?.close();
    } catch {
      /* already gone */
    }
    this.ws = null;
  }
}
