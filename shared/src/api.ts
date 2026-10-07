/**
 * REST client for the Alex Messages backend.
 *
 * One class, two auth modes: the web client relies on the HttpOnly
 * `am_session` cookie (sent automatically); the React Native client passes a
 * `token` which is sent as `Authorization: Bearer <token>` — /api/login
 * returns it as `session_token`.
 */

import type {
  Attachment,
  DmState,
  DmStatePayload,
  HistoryResponse,
  LinkPreview,
  LoginResponse,
  MeResponse,
  PublicUser,
} from "./types";

/** Error thrown for non-2xx API responses; `detail` is the server's message. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
  ) {
    super(detail || `HTTP ${status}`);
    this.name = "ApiError";
  }
}

export interface ApiClientOptions {
  /** Origin of the server; "" means same-origin (the web client). */
  baseUrl?: string;
  /** Bearer session token (native clients); null for cookie auth. */
  token?: string | null;
  /**
   * Native only: reads a `{ uri }` file's bytes. Expo's fetch (the default
   * on Expo SDK 57) can't stream React Native `{ uri }` FormData parts, but
   * accepts any part exposing `bytes()` — so uploads attach this reader.
   */
  readFile?: (uri: string) => Promise<Uint8Array>;
}

/**
 * A file to upload. Web passes a Blob/File; React Native passes the
 * `{ uri, name, type }` shape its FormData implementation understands.
 */
export type UploadFile = Blob | { uri: string; name: string; type: string };

function isNativeFile(f: UploadFile): f is { uri: string; name: string; type: string } {
  return typeof f === "object" && f !== null && "uri" in f;
}

export class ApiClient {
  baseUrl: string;
  token: string | null;
  private readFile?: (uri: string) => Promise<Uint8Array>;

  constructor(opts: ApiClientOptions = {}) {
    this.baseUrl = opts.baseUrl ?? "";
    this.token = opts.token ?? null;
    this.readFile = opts.readFile;
  }

  setToken(token: string | null): void {
    this.token = token;
  }

  /** URL for an API path — same-origin on web, absolute on native. */
  url(path: string): string {
    return this.baseUrl + path;
  }

  private headers(extra?: Record<string, string>): Record<string, string> {
    const h: Record<string, string> = { ...(extra || {}) };
    if (this.token) h["Authorization"] = `Bearer ${this.token}`;
    return h;
  }

  /** Raw fetch with auth wired in. */
  async request(path: string, init: RequestInit = {}): Promise<Response> {
    const headers = this.headers(init.headers as Record<string, string> | undefined);
    return fetch(this.url(path), {
      credentials: "include",
      ...init,
      headers,
    });
  }

  /** Fetch expecting a JSON body; throws ApiError with the server's detail. */
  async json<T>(path: string, init: RequestInit = {}): Promise<T> {
    const res = await this.request(path, init);
    const body = await res.json().catch(() => null);
    if (!res.ok) {
      const detail = (body && (body as { detail?: string }).detail) || `Request failed (${res.status})`;
      throw new ApiError(res.status, detail);
    }
    return body as T;
  }

  private post<T>(path: string, body?: unknown): Promise<T> {
    return this.json<T>(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }

  // ---------- auth ----------

  login(username: string, password: string): Promise<LoginResponse> {
    return this.post("/api/login", { username, password });
  }

  register(
    username: string,
    password: string,
    displayName?: string,
  ): Promise<{ id: number; status: string; message: string }> {
    return this.post("/api/register", {
      username,
      password,
      display_name: displayName,
    });
  }

  logout(): Promise<{ ok: boolean }> {
    return this.post("/api/logout");
  }

  // ---------- me ----------

  getMe(): Promise<MeResponse> {
    return this.json("/api/me");
  }

  patchMe(patch: { display_name?: string; bio?: string }): Promise<{ user: PublicUser }> {
    return this.json("/api/me", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    });
  }

  setAvatar(file: UploadFile): Promise<{ user: PublicUser }> {
    const fd = new FormData();
    appendFile(fd, file, this.readFile);
    return this.json("/api/me/avatar", { method: "POST", body: fd });
  }

  deleteAvatar(): Promise<{ user: PublicUser }> {
    return this.json("/api/me/avatar", { method: "DELETE" });
  }

  changePassword(currentPassword: string, newPassword: string): Promise<{ ok: boolean }> {
    return this.post("/api/me/password", {
      current_password: currentPassword,
      new_password: newPassword,
    });
  }

  deleteAccount(password: string): Promise<{ ok: boolean }> {
    return this.post("/api/me/delete", { password });
  }

  /** Downloads the personal data export as a parsed JSON object. */
  exportData(): Promise<unknown> {
    return this.json("/api/me/export");
  }

  // ---------- users & contacts ----------

  usersIndex(): Promise<{ users: PublicUser[] }> {
    return this.json("/api/users");
  }

  lookupUser(username: string): Promise<{ user: PublicUser }> {
    return this.json(`/api/users/lookup?username=${encodeURIComponent(username)}`);
  }

  contacts(): Promise<{ contacts: PublicUser[] }> {
    return this.json("/api/contacts");
  }

  addContact(userId: number): Promise<{ ok: boolean }> {
    return this.post(`/api/contacts/${userId}`);
  }

  removeContact(userId: number): Promise<{ ok: boolean }> {
    return this.json(`/api/contacts/${userId}`, { method: "DELETE" });
  }

  // ---------- messages ----------

  upload(file: UploadFile): Promise<Attachment> {
    const fd = new FormData();
    appendFile(fd, file, this.readFile);
    return this.json("/api/upload", { method: "POST", body: fd });
  }

  history(channel: string, before?: number, limit?: number): Promise<HistoryResponse> {
    const qs = new URLSearchParams();
    if (before != null) qs.set("before", String(before));
    if (limit != null) qs.set("limit", String(limit));
    const suffix = qs.size ? `?${qs.toString()}` : "";
    return this.json(`/api/history/${encodeURIComponent(channel)}${suffix}`);
  }

  /** Voice dictation: uploads an audio clip, returns its transcript. */
  transcribe(file: UploadFile): Promise<{ text: string }> {
    const fd = new FormData();
    appendFile(fd, file, this.readFile);
    return this.json("/api/transcribe", { method: "POST", body: fd });
  }

  linkPreview(url: string): Promise<LinkPreview | null> {
    return this.json<LinkPreview>(`/api/link-preview?url=${encodeURIComponent(url)}`).catch(() => null);
  }

  // ---------- per-user DM state ----------

  private dmAction(channel: string, action: string): Promise<Record<string, unknown>> {
    return this.post(`/api/dm/${encodeURIComponent(channel)}/${action}`);
  }

  pinDM(channel: string): Promise<unknown> {
    return this.dmAction(channel, "pin");
  }

  unpinDM(channel: string): Promise<unknown> {
    return this.dmAction(channel, "unpin");
  }

  markDMRead(channel: string): Promise<{ ok: boolean; last_read_at: number }> {
    return this.dmAction(channel, "read") as Promise<{ ok: boolean; last_read_at: number }>;
  }

  markDMUnread(channel: string): Promise<{ ok: boolean; last_read_at: number }> {
    return this.dmAction(channel, "unread") as Promise<{ ok: boolean; last_read_at: number }>;
  }

  deleteDM(channel: string): Promise<{ ok: boolean; cleared_at: number }> {
    return this.json(`/api/dm/${encodeURIComponent(channel)}`, { method: "DELETE" });
  }

  // ---------- web push ----------

  pushPublicKey(): Promise<{ public_key: string }> {
    return this.json("/api/push/public-key");
  }

  pushSubscribe(subscription: Record<string, unknown>, userAgent: string): Promise<unknown> {
    return this.post("/api/push/subscribe", { ...subscription, user_agent: userAgent });
  }

  pushUnsubscribe(endpoint: string): Promise<unknown> {
    return this.post("/api/push/unsubscribe", { endpoint });
  }

  // ---------- debug console ----------

  /** Fire-and-forget batched client events for the admin debug console. */
  debugReport(session: string, events: unknown[]): void {
    try {
      fetch(this.url("/api/debug/report"), {
        method: "POST",
        credentials: "include",
        keepalive: true,
        headers: this.headers({ "Content-Type": "application/json" }),
        body: JSON.stringify({ session, events }),
      }).catch(() => {});
    } catch {
      /* reporting must never throw */
    }
  }
}

/** dm_state wire payload → client-side camelCase DmState. */
export function dmStateFromPayload(s: Partial<DmStatePayload> | undefined, fallback?: DmState): DmState {
  const f: DmState = fallback ?? {
    pinned: false,
    lastReadAt: 0,
    clearedAt: 0,
    unreadCount: 0,
    peerLastReadAt: 0,
  };
  return {
    pinned: s?.pinned ?? f.pinned,
    lastReadAt: s?.last_read_at ?? f.lastReadAt,
    clearedAt: s?.cleared_at ?? f.clearedAt,
    unreadCount: s?.unread_count ?? f.unreadCount,
    peerLastReadAt: s?.peer_last_read_at ?? f.peerLastReadAt,
  };
}

/** Appends an UploadFile to FormData in the platform-appropriate way. */
function appendFile(
  fd: FormData,
  file: UploadFile,
  readFile?: (uri: string) => Promise<Uint8Array>,
): void {
  if (isNativeFile(file)) {
    // React Native's FormData accepts {uri,name,type} descriptors; Expo's
    // fetch instead pulls the part's bytes() — provide both.
    const part = readFile ? { ...file, bytes: () => readFile(file.uri) } : file;
    fd.append("file", part as unknown as Blob);
  } else {
    const name = file instanceof File ? file.name : "file";
    fd.append("file", file, name);
  }
}
