/**
 * ChatStore — the framework-agnostic state machine for Alex Messages.
 *
 * It owns every piece of chat state (mirroring the legacy app.js `state`
 * object), reduces server events into that state, and exposes the actions the
 * UI calls (send / edit / reply / pin / read / contacts …). Both the React
 * web app and the React Native app drive this same store, so behavior stays
 * identical across platforms.
 *
 * The store is deliberately framework-free: views subscribe and get a bumped
 * `version` counter on every change (React binds it via useSyncExternalStore).
 * Side effects the platform must render — toasts, reconnect banner, local
 * notifications — surface through small event hooks, not through the DOM.
 */

import { ApiClient } from "./api";
import { ChatSocket } from "./socket";
import { dmChannelFor, dmPeerOf, isDM, parseDMChannel } from "./dm";
import { dmStateFromPayload } from "./api";
import type {
  Attachment,
  BroadcastMessage,
  ClientMessage,
  DmState,
  DmThreadRef,
  HistoryMessage,
  LinkPreview,
  PublicUser,
  ServerEvent,
} from "./types";

/** How long to wait for the server's echo before flagging a send as failed. */
export const SEND_TIMEOUT_MS = 10000;
/** Messages kept per channel in memory (matches the legacy client). */
export const HISTORY_CAP = 200;
export const PAGE_SIZE_DEFAULT = 50;

/** A message plus client-side delivery state for optimistic sends. */
export interface ChatMessage extends BroadcastMessage {
  client_id?: string;
  _status?: "sending" | "delivered" | "failed";
}

export type ConnState = "connecting" | "online" | "reconnecting";

export interface ChatState {
  me: PublicUser | null;
  isAdmin: boolean;
  meCreatedAt: number;
  users: Record<number, PublicUser>;
  contacts: Set<number>;
  dmThreads: DmThreadRef[];
  online: Set<number>;
  history: Record<string, ChatMessage[]>;
  historyHasMore: Record<string, boolean>;
  historyLoading: Record<string, boolean>;
  dmState: Record<string, DmState>;
  linkPreviews: Record<string, LinkPreview | "none">;
  activeChannel: string | null;
  pendingAtt: Attachment[];
  replyTo: ChatMessage | null;
  connState: ConnState;
  pageSize: number;
  maxUpload: number;
  pushSubscribed: boolean;
  /** True once the first init frame has been applied. */
  ready: boolean;
}

const initialState = (): ChatState => ({
  me: null,
  isAdmin: false,
  meCreatedAt: 0,
  users: {},
  contacts: new Set(),
  dmThreads: [],
  online: new Set(),
  history: {},
  historyHasMore: {},
  historyLoading: {},
  dmState: {},
  linkPreviews: {},
  activeChannel: null,
  pendingAtt: [],
  replyTo: null,
  connState: "connecting",
  pageSize: PAGE_SIZE_DEFAULT,
  maxUpload: 20 * 1024 * 1024,
  pushSubscribed: false,
  ready: false,
});

export interface ToastEvent {
  text: string;
  isErr: boolean;
}

export interface NotifyEvent {
  channel: string;
  title: string;
  body: string;
}

export interface ChatStoreOptions {
  api: ApiClient;
  /** Builds the ws(s):// URL for /ws (platform-specific). */
  wsUrl: () => string;
  /** Bearer token for the socket (native); undefined = cookie auth. */
  token?: string | null;
  /** localStorage-compatible persistence for UI prefs (last channel etc). */
  storage?: KeyValueStore | null;
  /** Injectable WebSocket implementation (tests). */
  WebSocketImpl?: ConstructorParameters<typeof ChatSocket>[0]["WebSocketImpl"];
  /** Toast hook (banner messages). */
  onToast?: (t: ToastEvent) => void;
  /** Local-notification hook — fired for messages needing an OS notification
   *  (native clients; the web client relies on Web Push instead). */
  onNotify?: (n: NotifyEvent) => void;
  /** Debug reporter sink. */
  debug?: (level: string, event: string, message: string, context?: unknown) => void;
  /** Called when the session is rejected (WS close 4401 / 401 on /api/me). */
  onUnauthorized?: () => void;
}

/** Minimal storage contract so the store runs without window.localStorage. */
export interface KeyValueStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export const LAST_CHANNEL_KEY = "am_last_channel";

type Listener = () => void;

export class ChatStore {
  state: ChatState = initialState();
  /** Monotonic change counter — React subscribes to this. */
  version = 0;

  private api: ApiClient;
  private opts: ChatStoreOptions;
  private socket: ChatSocket | null = null;
  private listeners = new Set<Listener>();
  private pending: Record<string, { channel: string; timer: ReturnType<typeof setTimeout> | null }> = {};
  /** Whether the app surface is currently visible (drives read-marking). */
  private foreground = true;

  constructor(opts: ChatStoreOptions) {
    this.opts = opts;
    this.api = opts.api;
  }

  // ---------- plumbing ----------

  subscribe = (fn: Listener): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  private emit(): void {
    this.version++;
    for (const fn of this.listeners) fn();
  }

  private toast(text: string, isErr = false): void {
    this.opts.onToast?.({ text, isErr });
  }

  private debug(level: string, event: string, message: string, context?: unknown): void {
    this.opts.debug?.(level, event, message, context);
  }

  /** Platform tells the store whether the UI is in the foreground. */
  setForeground(visible: boolean): void {
    this.foreground = visible;
    if (visible && this.state.activeChannel && isDM(this.state.activeChannel) && this.isUnread(this.state.activeChannel)) {
      void this.markRead(this.state.activeChannel, { silent: true });
    }
  }

  // ---------- lifecycle ----------

  /** Opens the control socket. Safe to call once per session. */
  connect(): void {
    if (this.socket) return;
    this.socket = new ChatSocket({
      url: this.opts.wsUrl(),
      token: this.opts.token ?? null,
      WebSocketImpl: this.opts.WebSocketImpl,
      onEvent: (ev) => this.handleEvent(ev),
      onStatus: (status, info) => {
        if (status === "open") {
          this.state.connState = "online";
          this.emit();
        } else if (status === "connecting") {
          if (this.state.connState !== "reconnecting") {
            this.state.connState = "connecting";
            this.emit();
          }
        } else if (info?.code === 4401) {
          this.debug("warn", "ws_close", "Session expired (4401)");
          this.opts.onUnauthorized?.();
        } else {
          // Down for longer than the grace period — show the banner state.
          this.state.connState = "reconnecting";
          this.emit();
        }
      },
    });
    this.socket.connect();
  }

  disconnect(): void {
    this.socket?.destroy();
    this.socket = null;
  }

  private send(payload: ClientMessage): void {
    this.debug("debug", "ws_send", payload.type, "channel" in payload ? { channel: payload.channel } : undefined);
    this.socket?.send(payload);
  }

  // ---------- selectors ----------

  userFor(uid: number | null | undefined): PublicUser | null {
    if (uid == null) return null;
    return this.state.users[uid] || null;
  }

  dmStateFor(ch: string | null | undefined): DmState {
    if (!ch) return { pinned: false, lastReadAt: 0, clearedAt: 0, unreadCount: 0, peerLastReadAt: 0 };
    return (
      this.state.dmState[ch] || { pinned: false, lastReadAt: 0, clearedAt: 0, unreadCount: 0, peerLastReadAt: 0 }
    );
  }

  isUnread(ch: string | null | undefined): boolean {
    return this.dmStateFor(ch).unreadCount > 0;
  }

  peerOf(ch: string | null | undefined): number | null {
    return dmPeerOf(ch, this.state.me?.id);
  }

  channelExists(id: string | null | undefined): boolean {
    if (!id) return false;
    if (this.state.dmThreads.find((t) => t.channel === id)) return true;
    if (isDM(id)) {
      const peer = this.peerOf(id);
      return peer != null && !!this.state.users[peer];
    }
    return false;
  }

  /** Sidebar items: dmThreads ∪ contacts, sorted recent-first. */
  dmListItems(): { channel: string; peerId: number }[] {
    const seen = new Set<string>();
    const items: { channel: string; peerId: number }[] = [];
    for (const t of this.state.dmThreads) {
      if (seen.has(t.channel)) continue;
      seen.add(t.channel);
      items.push({ channel: t.channel, peerId: t.peer_id });
    }
    for (const cid of this.state.contacts) {
      const me = this.state.me;
      if (!me) continue;
      const ch = dmChannelFor(me.id, cid);
      if (seen.has(ch)) continue;
      seen.add(ch);
      items.push({ channel: ch, peerId: cid });
    }
    items.sort((a, b) => {
      const ta = this.lastMessageTS(a.channel);
      const tb = this.lastMessageTS(b.channel);
      if (ta !== tb) return tb - ta;
      const na = this.userFor(a.peerId)?.display_name || "";
      const nb = this.userFor(b.peerId)?.display_name || "";
      return na.localeCompare(nb);
    });
    return items;
  }

  /** Timestamp of the newest message in a thread (0 when empty). */
  lastMessageTS(channel: string): number {
    const arr = this.state.history[channel] || [];
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].type !== "system") return arr[i].created_at || 0;
    }
    return 0;
  }

  /** Delivery remark under the newest own message: Sending…/Delivered/Read. */
  deliveryStatus(channel: string | null): { messageId: string; label: "Sending…" | "Delivered" | "Read" } | null {
    if (!channel || !isDM(channel) || !this.state.me) return null;
    const arr = this.state.history[channel] || [];
    const st = this.dmStateFor(channel);
    let lastOwn: ChatMessage | null = null;
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].type === "system") continue;
      lastOwn = arr[i].user_id === this.state.me.id ? arr[i] : null;
      break;
    }
    if (!lastOwn || lastOwn._status === "failed") return null;
    if (st.peerLastReadAt && (lastOwn.created_at || 0) <= st.peerLastReadAt) {
      return { messageId: lastOwn.id, label: "Read" };
    }
    return { messageId: lastOwn.id, label: lastOwn._status === "sending" ? "Sending…" : "Delivered" };
  }

  // ---------- event reduction ----------

  handleEvent(data: ServerEvent): void {
    switch (data.type) {
      case "init":
        this.applyInit(data);
        break;
      case "message":
        this.applyMessage(data.channel, data.message);
        break;
      case "message_edited":
        this.applyEdited(data.channel, data.id, data.text, data.edited_at);
        break;
      case "presence":
        this.state.online = new Set(data.online || []);
        this.emit();
        break;
      case "profile_update":
        this.applyProfileUpdate(data.profile);
        break;
      case "dm_read":
        this.applyDmRead(data.channel, data.user_id, data.last_read_at);
        break;
      case "dm_opened":
        this.applyDmOpened(data);
        break;
      case "pong":
        break;
    }
  }

  private applyInit(data: Extract<ServerEvent, { type: "init" }>): void {
    const s = this.state;
    s.me = data.me;
    s.users = {};
    for (const u of data.users || []) s.users[u.id] = u;
    if (s.me) s.users[s.me.id] = s.me;
    s.contacts = new Set(data.contacts || []);
    s.dmThreads = data.dm_threads || [];
    s.online = new Set(data.online || []);
    s.history = (data.history || {}) as Record<string, ChatMessage[]>;
    s.historyHasMore = data.history_has_more || {};
    s.historyLoading = {};
    s.pageSize = data.page_size || PAGE_SIZE_DEFAULT;
    s.maxUpload = data.max_upload || s.maxUpload;
    s.dmState = {};
    for (const [ch, st] of Object.entries(data.dm_state || {})) {
      s.dmState[ch] = dmStateFromPayload(st);
    }
    const saved = this.readLastChannel();
    s.activeChannel = this.channelExists(saved) ? saved : null;
    s.ready = true;
    this.debug("info", "init", "Session initialized", {
      users: (data.users || []).length,
      threads: (data.dm_threads || []).length,
    });
    this.emit();

    // Fetch admin flag + account creation time (the init frame omits them).
    this.api
      .getMe()
      .then((j) => {
        this.state.isAdmin = !!j.is_admin;
        this.state.meCreatedAt = j.created_at || 0;
        this.emit();
      })
      .catch(() => {});

    if (s.activeChannel) {
      this.send({ type: "switch", channel: s.activeChannel });
      if (isDM(s.activeChannel) && this.isUnread(s.activeChannel)) {
        void this.markRead(s.activeChannel, { silent: true });
      }
    }
  }

  private applyMessage(channel: string, msg: BroadcastMessage): void {
    const s = this.state;
    if (msg.author && msg.author.id != null) {
      s.users[msg.author.id] = { ...(s.users[msg.author.id] || ({} as PublicUser)), ...msg.author };
    }

    // Reconcile our own optimistic bubble (matched by the echoed client_id).
    if (msg.client_id && this.pending[msg.client_id] && s.me && msg.user_id === s.me.id) {
      const cid = msg.client_id;
      const p = this.pending[cid];
      if (p?.timer) clearTimeout(p.timer);
      delete this.pending[cid];
      const arr = s.history[channel] || [];
      const local = arr.find((x) => x.client_id === cid);
      if (local) {
        local.id = msg.id;
        local.created_at = msg.created_at;
        local.edited_at = msg.edited_at ?? null;
        local._status = "delivered";
        this.emit();
        return;
      }
      // Optimistic bubble already evicted — fall through to append.
    }

    if (msg.user_id != null && !s.online.has(msg.user_id)) {
      s.online.add(msg.user_id);
    }
    const arr = (s.history[channel] = s.history[channel] || []);
    arr.push(msg as ChatMessage);
    while (arr.length > HISTORY_CAP) arr.shift();

    if (isDM(channel) && !s.dmThreads.find((t) => t.channel === channel)) {
      const peer = this.peerOf(channel);
      if (peer != null) s.dmThreads.push({ channel, peer_id: peer });
    }

    let notify: NotifyEvent | null = null;
    if (isDM(channel) && msg.user_id != null && s.me && msg.user_id !== s.me.id) {
      const st = this.dmStateFor(channel);
      if (channel === s.activeChannel && this.foreground) {
        // Actively reading — mark read instead of incrementing unread.
        s.dmState[channel] = {
          ...st,
          lastReadAt: msg.created_at || Math.floor(Date.now() / 1000),
          unreadCount: 0,
        };
        void this.markRead(channel, { silent: true });
      } else {
        s.dmState[channel] = { ...st, unreadCount: (st.unreadCount || 0) + 1 };
        notify = {
          channel,
          title: msg.author?.display_name || "New message",
          body: msg.text
            ? msg.text.replace(/\s+/g, " ").slice(0, 120)
            : msg.attachments?.length
              ? `Sent ${msg.attachments.length === 1 ? "a file" : `${msg.attachments.length} files`}`
              : "New message",
        };
      }
    }
    this.emit();
    if (notify) this.opts.onNotify?.(notify);
  }

  private applyEdited(channel: string, id: string, text: string, editedAt: number): void {
    const arr = this.state.history[channel] || [];
    const m = arr.find((x) => x.id === id);
    if (m) {
      m.text = text;
      m.edited_at = editedAt;
    }
    this.emit();
  }

  private applyProfileUpdate(p: PublicUser): void {
    const s = this.state;
    const isSelf = s.me && p.id === s.me.id;
    if (!isSelf && !s.users[p.id]) return; // ignore strangers
    s.users[p.id] = { ...(s.users[p.id] || ({} as PublicUser)), ...p };
    if (isSelf) s.me = { ...s.me, ...p };
    this.emit();
  }

  private applyDmRead(channel: string, userId: number, lastReadAt: number): void {
    const st = this.dmStateFor(channel);
    if (this.state.me && userId === this.state.me.id) {
      this.state.dmState[channel] = { ...st, lastReadAt: lastReadAt || st.lastReadAt, unreadCount: 0 };
    } else {
      this.state.dmState[channel] = { ...st, peerLastReadAt: lastReadAt || 0 };
    }
    this.emit();
  }

  private applyDmOpened(data: Extract<ServerEvent, { type: "dm_opened" }>): void {
    const s = this.state;
    s.history[data.channel] = (data.history || []) as ChatMessage[];
    s.historyHasMore[data.channel] = !!data.has_more;
    if (!s.dmThreads.find((t) => t.channel === data.channel)) {
      s.dmThreads.push({ channel: data.channel, peer_id: data.peer_id });
    }
    if (data.state) {
      s.dmState[data.channel] = dmStateFromPayload(data.state, this.dmStateFor(data.channel));
    }
    this.emit();
  }

  // ---------- actions ----------

  /** Selects a channel: switches the view, tells the server, marks it read. */
  switchChannel(id: string | null): void {
    if (this.state.activeChannel === id) return;
    this.state.activeChannel = id;
    this.writeLastChannel(id);
    this.state.replyTo = null;
    this.emit();
    if (id) {
      this.send({ type: "switch", channel: id });
      if (isDM(id) && this.isUnread(id)) void this.markRead(id, { silent: true });
    }
  }

  /** Opens (or creates) the DM with a peer and switches to it. */
  openDM(peerId: number): void {
    const me = this.state.me;
    if (!me || peerId === me.id) return;
    const ch = dmChannelFor(me.id, peerId);
    if (!this.state.dmThreads.find((t) => t.channel === ch)) {
      this.state.dmThreads.push({ channel: ch, peer_id: peerId });
    }
    this.send({ type: "open_dm", peer_id: peerId });
    this.switchChannel(ch);
  }

  setReplyTo(m: ChatMessage | null): void {
    this.state.replyTo = m;
    this.emit();
  }

  setPendingAtt(atts: Attachment[]): void {
    this.state.pendingAtt = atts;
    this.emit();
  }

  addPendingAtt(a: Attachment): void {
    this.state.pendingAtt = [...this.state.pendingAtt, a];
    this.emit();
  }

  removePendingAtt(index: number): void {
    const atts = [...this.state.pendingAtt];
    atts.splice(index, 1);
    this.state.pendingAtt = atts;
    this.emit();
  }

  /** Uploads a file and stages it as a pending attachment. */
  async uploadAttachment(file: Parameters<ApiClient["upload"]>[0], name: string): Promise<boolean> {
    try {
      const j = await this.api.upload(file);
      this.addPendingAtt({
        name: j.name || name,
        url: j.url,
        size: j.size,
        mime: j.mime,
        width: j.width || 0,
        height: j.height || 0,
      });
      return true;
    } catch {
      this.toast(`Upload failed: ${name}`, true);
      return false;
    }
  }

  /** Sends the composer contents (text + pending attachments) optimistically. */
  sendMessage(text: string, attachments?: Attachment[]): { clientId: string } | null {
    const s = this.state;
    if (!s.activeChannel || !s.me) return null;
    const trimmed = text.trim();
    const atts = attachments ?? s.pendingAtt;
    if (!trimmed && !atts.length) return null;
    const channel = s.activeChannel;
    const cid = "c" + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
    const optimistic: ChatMessage = {
      id: cid,
      client_id: cid,
      type: "message",
      channel,
      user_id: s.me.id,
      text: trimmed,
      reply_to: s.replyTo ? s.replyTo.id : null,
      attachments: atts.slice(),
      created_at: Math.floor(Date.now() / 1000),
      edited_at: null,
      _status: "sending",
    };
    const arr = (s.history[channel] = s.history[channel] || []);
    arr.push(optimistic);
    while (arr.length > HISTORY_CAP) arr.shift();
    if (isDM(channel) && !s.dmThreads.find((t) => t.channel === channel)) {
      const peer = this.peerOf(channel);
      if (peer != null) s.dmThreads.push({ channel, peer_id: peer });
    }
    this.send({
      type: "message",
      channel,
      text: trimmed,
      reply_to: optimistic.reply_to,
      attachments: optimistic.attachments,
      client_id: cid,
    });
    this.pending[cid] = { channel, timer: setTimeout(() => this.markSendFailed(channel, cid), SEND_TIMEOUT_MS) };
    s.pendingAtt = [];
    s.replyTo = null;
    this.emit();
    return { clientId: cid };
  }

  private findOptimistic(channel: string, cid: string): ChatMessage | undefined {
    return (this.state.history[channel] || []).find((m) => m.client_id === cid);
  }

  private markSendFailed(channel: string, cid: string): void {
    const p = this.pending[cid];
    if (p?.timer) clearTimeout(p.timer);
    if (this.pending[cid]) this.pending[cid] = { channel, timer: null };
    const m = this.findOptimistic(channel, cid);
    if (!m) return;
    m._status = "failed";
    this.emit();
  }

  /** Re-sends a failed optimistic message with the same client_id nonce. */
  retrySend(cid: string): void {
    const p = this.pending[cid];
    const channel = p ? p.channel : this.state.activeChannel;
    if (!channel) return;
    const m = this.findOptimistic(channel, cid);
    if (!m) return;
    m._status = "sending";
    this.send({
      type: "message",
      channel,
      text: m.text,
      reply_to: m.reply_to,
      attachments: m.attachments,
      client_id: cid,
    });
    this.pending[cid] = { channel, timer: setTimeout(() => this.markSendFailed(channel, cid), SEND_TIMEOUT_MS) };
    this.emit();
  }

  /** Edits one of our own messages (optimistic local apply + server edit). */
  editMessage(m: ChatMessage, text: string): void {
    const trimmed = text.trim();
    if (!trimmed || trimmed === m.text) return;
    this.send({ type: "edit", id: m.id, text: trimmed });
    m.text = trimmed;
    m.edited_at = Math.floor(Date.now() / 1000);
    this.emit();
  }

  /** Lazily loads the previous page of history for a channel. */
  async loadOlder(channel: string): Promise<void> {
    const s = this.state;
    if (!channel || s.historyLoading[channel] || !s.historyHasMore[channel]) return;
    const arr = s.history[channel] || [];
    const oldest = arr.length ? arr[0].created_at : null;
    if (oldest == null) return;
    s.historyLoading[channel] = true;
    this.emit();
    try {
      const j = await this.api.history(channel, oldest, s.pageSize);
      const older = (j.messages || []) as ChatMessage[];
      if (!older.length) {
        s.historyHasMore[channel] = false;
      } else {
        const seen = new Set(arr.map((m) => m.id));
        s.history[channel] = [...older.filter((m) => !seen.has(m.id)), ...arr];
        s.historyHasMore[channel] = !!j.has_more;
      }
    } catch {
      /* leave hasMore so a later scroll retries */
    } finally {
      s.historyLoading[channel] = false;
      this.emit();
    }
  }

  async togglePin(channel: string, pinned: boolean): Promise<void> {
    try {
      if (pinned) await this.api.pinDM(channel);
      else await this.api.unpinDM(channel);
      const st = this.dmStateFor(channel);
      this.state.dmState[channel] = { ...st, pinned };
      this.emit();
      this.toast(pinned ? "Pinned" : "Unpinned");
    } catch {
      this.toast("Couldn't update pin", true);
    }
  }

  async markRead(channel: string, opts: { silent?: boolean } = {}): Promise<void> {
    try {
      const j = await this.api.markDMRead(channel);
      const st = this.dmStateFor(channel);
      this.state.dmState[channel] = {
        ...st,
        lastReadAt: j.last_read_at || Math.floor(Date.now() / 1000),
        unreadCount: 0,
      };
      this.emit();
    } catch {
      if (!opts.silent) this.toast("Couldn't mark as read", true);
    }
  }

  async markUnread(channel: string): Promise<void> {
    try {
      await this.api.markDMUnread(channel);
      const st = this.dmStateFor(channel);
      const arr = this.state.history[channel] || [];
      const myId = this.state.me?.id;
      const unreadCount =
        arr.filter((m) => m.user_id != null && m.user_id !== myId && m.type !== "system").length || 1;
      this.state.dmState[channel] = { ...st, lastReadAt: 0, unreadCount };
      this.emit();
      this.toast("Marked as unread");
    } catch {
      this.toast("Couldn't mark as unread", true);
    }
  }

  /** Delete-for-me: hides all existing messages in this thread for this user. */
  async deleteDm(channel: string): Promise<void> {
    try {
      const j = await this.api.deleteDM(channel);
      const cleared = j.cleared_at || Math.floor(Date.now() / 1000);
      this.state.history[channel] = [];
      this.state.historyHasMore[channel] = false;
      this.state.dmState[channel] = {
        pinned: false,
        lastReadAt: cleared,
        clearedAt: cleared,
        unreadCount: 0,
        peerLastReadAt: 0,
      };
      this.state.dmThreads = this.state.dmThreads.filter((t) => t.channel !== channel);
      if (this.state.activeChannel === channel) {
        this.state.activeChannel = null;
        this.writeLastChannel(null);
      }
      this.emit();
      this.toast("Conversation deleted");
    } catch {
      this.toast("Couldn't delete", true);
    }
  }

  async toggleContact(uid: number): Promise<void> {
    try {
      if (this.state.contacts.has(uid)) {
        await this.api.removeContact(uid);
        this.state.contacts.delete(uid);
        this.toast("Contact removed");
      } else {
        await this.api.addContact(uid);
        this.state.contacts.add(uid);
        this.toast("Added to contacts");
      }
      this.emit();
    } catch {
      this.toast("Contact update failed", true);
    }
  }

  /** Applies a fresh own profile (from /api/me PATCH, avatar routes, login). */
  applyOwnProfile(user: PublicUser): void {
    if (!this.state.me) return;
    this.state.me = { ...this.state.me, ...user };
    this.state.users[this.state.me.id] = { ...(this.state.users[this.state.me.id] || user), ...user };
    this.emit();
  }

  async saveProfile(name: string, bio: string): Promise<boolean> {
    try {
      const j = await this.api.patchMe({ display_name: name, bio });
      this.applyOwnProfile(j.user);
      this.toast("Profile saved");
      return true;
    } catch {
      this.toast("Save failed", true);
      return false;
    }
  }

  async lookupAndOpen(username: string): Promise<{ ok: true } | { ok: false; error: string }> {
    const uname = username.trim().replace(/^@/, "");
    if (!uname) return { ok: false, error: "Enter a username." };
    try {
      const { user } = await this.api.lookupUser(uname);
      this.state.users[user.id] = { ...(this.state.users[user.id] || user), ...user };
      this.openDM(user.id);
      return { ok: true };
    } catch (e) {
      const err = e as { status?: number };
      if (err.status === 404) return { ok: false, error: "No user with that username." };
      if (err.status === 400) return { ok: false, error: "That's your own username." };
      return { ok: false, error: "Lookup failed. Try again." };
    }
  }

  /** Link-preview fetch with the store's negative cache. */
  async linkPreview(url: string): Promise<LinkPreview | null> {
    const cached = this.state.linkPreviews[url];
    if (cached === "none") return null;
    if (cached) return cached;
    const p = await this.api.linkPreview(url);
    if (!p || (!p.title && !p.image)) {
      this.state.linkPreviews[url] = "none";
      return null;
    }
    this.state.linkPreviews[url] = p;
    this.emit();
    return p;
  }

  /** Push-received hook (web service worker): bumps unread eagerly. */
  handlePushReceived(channel: string): void {
    if (isDM(channel) && channel !== this.state.activeChannel) {
      const st = this.dmStateFor(channel);
      this.state.dmState[channel] = { ...st, unreadCount: (st.unreadCount || 0) + 1 };
      this.emit();
    }
  }

  /** Channel-open request coming from an external surface (push tap, deep link). */
  openChannelExternal(channel: string): void {
    if (isDM(channel)) {
      const peer = this.peerOf(channel);
      if (peer != null && !this.state.dmThreads.find((t) => t.channel === channel)) {
        this.state.dmThreads.push({ channel, peer_id: peer });
      }
    }
    this.switchChannel(channel);
  }

  /** Full reset — used on logout. */
  reset(): void {
    for (const p of Object.values(this.pending)) {
      if (p.timer) clearTimeout(p.timer);
    }
    this.pending = {};
    this.disconnect();
    this.state = initialState();
    this.emit();
  }

  // ---------- persistence ----------

  private readLastChannel(): string | null {
    try {
      return this.opts.storage?.getItem(LAST_CHANNEL_KEY) || null;
    } catch {
      return null;
    }
  }

  private writeLastChannel(id: string | null): void {
    try {
      if (id) this.opts.storage?.setItem(LAST_CHANNEL_KEY, id);
      else this.opts.storage?.removeItem(LAST_CHANNEL_KEY);
    } catch {
      /* private mode */
    }
  }
}
