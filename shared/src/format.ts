/**
 * Display formatting helpers, ported from the legacy static/app.js so every
 * client renders identically: peer avatar colors, initials, timestamps,
 * sidebar previews, and message-body segments.
 *
 * The peer-color algorithm is part of the wire contract in spirit — the web
 * app, the Kotlin client, and these clients must all paint the same account
 * the same color. Keep PEER_COLORS and hash() in sync across platforms.
 */

import type { Attachment, HistoryMessage, PublicUser } from "./types";

/** Sage-family palette for generated avatars (matches web app.js). */
export const PEER_COLORS = [
  "#4F7A5E", "#7BA17F", "#39604A", "#A3B581", "#6F8A6E",
  "#8FAE92", "#5A7773", "#94A36B", "#5B8A6C", "#6A8E5F",
];

/** 32-bit rolling hash identical to the web client's hash(). */
export function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/** Deterministic avatar color for a user id. */
export function colorFor(uid: number | null | undefined): string {
  const key = String(uid ?? "?");
  return PEER_COLORS[hash(key) % PEER_COLORS.length];
}

/** Lighten (amt > 0) or darken (amt < 0) a #rrggbb color; -1..1 range. */
export function shade(hex: string, amt: number): string {
  const c = hex.replace("#", "");
  const r = parseInt(c.slice(0, 2), 16);
  const g = parseInt(c.slice(2, 4), 16);
  const b = parseInt(c.slice(4, 6), 16);
  const adj = (v: number) =>
    Math.max(0, Math.min(255, Math.round(v + (amt < 0 ? v * amt : (255 - v) * amt))));
  return `#${[adj(r), adj(g), adj(b)].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

export function nameFor(users: Record<number, PublicUser>, uid: number | null | undefined): string {
  if (uid == null) return "Deleted User";
  const u = users[uid];
  return (u && u.display_name) || (u && u.username) || "Deleted User";
}

export function handleFor(users: Record<number, PublicUser>, uid: number | null | undefined): string {
  if (uid == null) return "";
  const u = users[uid];
  return u ? "@" + u.username : "";
}

export function initialsFor(users: Record<number, PublicUser>, uid: number | null | undefined): string {
  if (uid == null || !users[uid]) return "?";
  const name = nameFor(users, uid);
  const parts = name.split(/[\s._-]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return (parts[0] || "?").slice(0, 2).toUpperCase();
}

export function fmtSize(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(2) + " MB";
}

/** "14:03" — the small timestamp inside bubbles. */
export function fmtTime(ts: number | null | undefined): string {
  if (!ts) return "";
  const d = new Date(ts * 1000);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/** Whole local calendar days between `ts` and `now` (0 = same day). */
function daysAgo(ts: number, now: Date): number {
  return Math.round((startOfDay(now) - startOfDay(new Date(ts * 1000))) / 86_400_000);
}

/**
 * Conversation-list timestamp, iMessage style: "10:19 PM" today,
 * "Yesterday", a weekday within the last week, otherwise a short date.
 */
export function fmtListTime(ts: number | null | undefined, now: Date = new Date()): string {
  if (!ts) return "";
  const days = daysAgo(ts, now);
  const d = new Date(ts * 1000);
  if (days <= 0) return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  if (days === 1) return "Yesterday";
  if (days < 7) return d.toLocaleDateString([], { weekday: "long" });
  return d.toLocaleDateString([], { month: "numeric", day: "numeric", year: "2-digit" });
}

/**
 * Centered in-stream timestamp header: "Today 10:19 PM", "Yesterday 9:03 AM",
 * "Monday 9:03 AM", or "Sep 17, 9:03 AM".
 */
export function fmtStampLabel(ts: number | null | undefined, now: Date = new Date()): string {
  if (!ts) return "";
  const days = daysAgo(ts, now);
  const d = new Date(ts * 1000);
  const time = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  if (days <= 0) return `Today ${time}`;
  if (days === 1) return `Yesterday ${time}`;
  if (days < 7) return `${d.toLocaleDateString([], { weekday: "long" })} ${time}`;
  return `${d.toLocaleDateString([], { month: "short", day: "numeric" })}, ${time}`;
}

/** "2026-09-17" — used for day separators. */
export function isoDate(ts: number | null | undefined): string {
  if (!ts) return "";
  return new Date(ts * 1000).toISOString().slice(0, 10);
}

/** "Today" or "Wednesday, Sep 17" for the day separator pill. */
export function formatDate(d: string): string {
  if (!d) return "";
  const today = new Date().toISOString().slice(0, 10);
  if (d === today) return "Today";
  const dt = new Date(d + "T00:00:00");
  return dt.toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" });
}

/** "Mar 5, 2026" — for account info rows. */
export function fmtAccountDate(ts: number): string {
  if (!ts) return "—";
  try {
    return new Date(ts * 1000).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return "—";
  }
}

/**
 * Attachment rendering bucket. "image" covers GIFs (inline <img>/Image),
 * "video" and "audio" get their own players, anything else is a file card.
 */
export type AttachmentKind = "image" | "video" | "audio" | "file";

export function attachmentKind(a: Pick<Attachment, "mime" | "name">): AttachmentKind {
  const mime = a.mime || "";
  if (mime.startsWith("image/")) return "image";
  if (mime.startsWith("video/")) return "video";
  if (mime.startsWith("audio/")) return "audio";
  // Servers or clients that don't label the mime still get the right player.
  const ext = (a.name.split(".").pop() || "").toLowerCase();
  if (["mp4", "mov", "webm", "m4v", "mkv", "avi"].includes(ext)) return "video";
  if (["mp3", "wav", "m4a", "aac", "ogg", "oga", "flac", "caf", "opus", "weba"].includes(ext)) return "audio";
  return "file";
}

/**
 * File name every client gives a recorded voice message (`voice-message.wav`
 * on web/iOS, `.m4a` on Android). The name is the marker that separates a
 * voice message (rendered as a waveform player) from an audio file the user
 * uploaded (rendered as a named audio card).
 */
export const VOICE_MESSAGE_BASENAME = "voice-message";

export function isVoiceMessage(a: Pick<Attachment, "mime" | "name">): boolean {
  if (attachmentKind(a) !== "audio") return false;
  const name = a.name || "";
  const dot = name.lastIndexOf(".");
  return (dot < 0 ? name : name.slice(0, dot)) === VOICE_MESSAGE_BASENAME;
}

/** Compact description of one attachment for previews: "📎 photo.jpg" style
 *  handled by callers; this returns the noun. */
export function attachmentLabel(a: Pick<Attachment, "mime" | "name">): string {
  switch (attachmentKind(a)) {
    case "image":
      return "Photo";
    case "video":
      return "Video";
    case "audio":
      return isVoiceMessage(a) ? "Voice message" : "Audio";
    default:
      return "Attachment";
  }
}

/** One-line preview of the newest message in a thread (for the sidebar). */
export function lastMessagePreviewFor(
  messages: HistoryMessage[] | undefined,
  meId: number | null,
): string {
  const arr = messages || [];
  for (let i = arr.length - 1; i >= 0; i--) {
    const m = arr[i];
    if (m.type === "system") continue;
    const prefix = meId != null && m.user_id === meId ? "You: " : "";
    if (m.text) return prefix + m.text.replace(/\s+/g, " ").slice(0, 80);
    if (m.attachments && m.attachments.length) {
      const a = m.attachments[0];
      return prefix + (isVoiceMessage(a) ? "Voice message" : "Attachment: " + attachmentKind(a));
    }
    return "";
  }
  return "";
}

/** Timestamp of the newest non-system message (sidebar sort key). */
export function lastMessageTSFor(messages: HistoryMessage[] | undefined): number {
  const arr = messages || [];
  for (let i = arr.length - 1; i >= 0; i--) {
    if (arr[i].type !== "system") return arr[i].created_at || 0;
  }
  return 0;
}

// ---------- message body segments ----------

/**
 * The body renderer emits typed segments instead of HTML so React and React
 * Native can both render safely without string markup:
 *   `code`   — inline code span (backtick-quoted)
 *   `link`   — an http(s) URL (rendered as a tappable anchor)
 *   `br`     — a newline
 *   `text`   — everything else
 */
export type BodySegment =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "link"; text: string; href: string }
  | { kind: "br" };

const CODE_RE = /`([^`\n]+)`/g;
const URL_RE = /(https?:\/\/[^\s<]+)/g;

export function bodySegments(text: string): BodySegment[] {
  const out: BodySegment[] = [];
  // Split out backtick code spans first; the remainder is plain text.
  let last = 0;
  for (const m of text.matchAll(CODE_RE)) {
    const idx = m.index ?? 0;
    if (idx > last) pushTextWithLinks(text.slice(last, idx), out);
    out.push({ kind: "code", text: m[1] });
    last = idx + m[0].length;
  }
  if (last < text.length) pushTextWithLinks(text.slice(last), out);
  return out;
}

function pushTextWithLinks(chunk: string, out: BodySegment[]): void {
  let last = 0;
  for (const m of chunk.matchAll(URL_RE)) {
    const idx = m.index ?? 0;
    if (idx > last) pushTextWithBreaks(chunk.slice(last, idx), out);
    out.push({ kind: "link", text: m[0], href: m[0] });
    last = idx + m[0].length;
  }
  if (last < chunk.length) pushTextWithBreaks(chunk.slice(last), out);
}

function pushTextWithBreaks(chunk: string, out: BodySegment[]): void {
  const parts = chunk.split("\n");
  parts.forEach((p, i) => {
    if (i > 0) out.push({ kind: "br" });
    if (p) out.push({ kind: "text", text: p });
  });
}

/** First URL in a message body, for link-preview hydration. */
export function firstUrl(text: string): string | null {
  const m = (text || "").match(/(https?:\/\/[^\s<]+)/);
  return m ? m[1] : null;
}
