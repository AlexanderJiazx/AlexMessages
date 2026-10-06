/**
 * Debug reporter — batches client-side events and streams them to the admin
 * debug console via POST /api/debug/report. Mirrors the Debug IIFE in the
 * legacy app.js: 2 s batching, 25-event flush trigger, 50-event cap per post.
 */

import type { ApiClient } from "./api";

export interface DebugEvent {
  ts_ms: number;
  level: string;
  event: string;
  message: string;
  context?: unknown;
}

export interface DebugReporterOptions {
  api: ApiClient;
  /** Storage for the per-tab session id (sessionStorage on web). */
  sessionStorage?: { getItem(k: string): string | null; setItem(k: string, v: string): void } | null;
}

const SESSION_KEY = "am_dbg_session";

export class DebugReporter {
  private session = "anon";
  private queue: DebugEvent[] = [];
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private opts: DebugReporterOptions) {
    try {
      const store = opts.sessionStorage;
      let s = store?.getItem(SESSION_KEY) || "";
      if (!s) {
        s = Math.random().toString(36).slice(2, 10);
        store?.setItem(SESSION_KEY, s);
      }
      this.session = s;
    } catch {
      /* storage unavailable */
    }
  }

  flush = (): void => {
    this.timer = null;
    if (!this.queue.length) return;
    const events = this.queue.splice(0, 50);
    this.opts.api.debugReport(this.session, events);
  };

  log(level: string, event: string, message?: string, context?: unknown): void {
    this.queue.push({
      ts_ms: Date.now(),
      level,
      event,
      message: message == null ? "" : String(message),
      context,
    });
    if (this.queue.length >= 25) this.flush();
    else if (!this.timer) this.timer = setTimeout(this.flush, 2000);
  }

  debug = (event: string, message?: string, context?: unknown): void =>
    this.log("debug", event, message, context);
  info = (event: string, message?: string, context?: unknown): void =>
    this.log("info", event, message, context);
  warn = (event: string, message?: string, context?: unknown): void =>
    this.log("warn", event, message, context);
  error = (event: string, message?: string, context?: unknown): void =>
    this.log("error", event, message, context);
}
