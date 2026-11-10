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
import { CallManager, type CallPlatform, type CallSession } from "./call";
import { ChatSocket } from "./socket";
import { dmChannelFor, dmPeerOf, isDM, parseDMChannel } from "./dm";
import { dmStateFromPayload } from "./api";
import { callMissedBy } from "./format";
import type {
  Attachment,
  BroadcastMessage,
  CallMedia,
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
export const PAGE_SIZE_DEFAULT = 50;
/** Most attachments one message may carry, until init says otherwise. */
export const MAX_ATTACHMENTS_DEFAULT = 6;

/**
 * A message plus client-side delivery state for optimistic sends.
 *
 * Messages and history arrays are immutable: every change replaces the
 * message object and the channel's array, so views can memoize on identity.
 * Render lists keyed by `messageKey(m)`, which stays the same when an
 * optimistic message is confirmed and its `id` changes to the server's.
 */
export interface ChatMessage extends BroadcastMessage {
  client_id?: string;
  _status?: "sending" | "delivered" | "failed";
}

/** Stable list key: the send nonce when there is one, else the id. */
export function messageKey(m: { id: string; client_id?: string }): string {
  return m.client_id || m.id;
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
  maxAttachments: number;
  pushSubscribed: boolean;
  /** True once the first init frame has been applied. */
  ready: boolean;
  /** This device's call — live, or just ended (see CallManager). */
  call: CallSession | null;
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
  maxAttachments: MAX_ATTACHMENTS_DEFAULT,
  pushSubscribed: false,
  ready: false,
  call: null,
});

export interface ToastEvent {
  text: string;
  isErr: boolean;
}

export interface NotifyEvent {
  channel: string;
  title: string;
  body: string;
  /** "call": a call is ringing while the app is in the background;
   *  "missed_call": the message that logs a call this user missed. */
  kind?: "message" | "call" | "missed_call";
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
  /** WebRTC for calls; null/absent = this client can't place or take calls. */
  callPlatform?: CallPlatform | null;
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
  private pending: Record<string, { channel: string; timer: ReturnType<typeof setTimeout> | null; message: ChatMessage; edit?: string }> = {};
  /** Bumped whenever a channel's history is replaced by a server snapshot
   *  or cleared, so a history page that was requested before is dropped. */
  private historyEpoch: Record<string, number> = {};
  /** Whether the app surface is currently visible (drives read-marking). */
  private foreground = true;
  /** This device's side of DM calls; its session is mirrored in state.call. */
  readonly calls: CallManager;

  constructor(opts: ChatStoreOptions) {
    this.opts = opts;
    this.api = opts.api;
    this.calls = new CallManager({
      platform: opts.callPlatform ?? null,
      send: (m) => this.send(m),
      me: () => this.state.me,
      changed: (session) => {
        this.state.call = session;
        this.emit();
      },
      toast: (text, isErr) => this.toast(text, isErr),
      debug: (level, event, message, context) => this.debug(level, event, message, context),
      foreground: () => this.foreground,
      notifyIncoming: (session) =>
        this.opts.onNotify?.({
          channel: session.channel,
          title: session.peer.display_name || session.peer.username,
          body: session.media === "video" ? "Incoming video call" : "Incoming voice call",
          kind: "call",
        }),
    });
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

  /** Replaces the first message of `channel` that matches with `next(old)`,
   *  copy-on-write, and keeps a pending send's reference current. */
  private updateMessage(channel: string, match: (m: ChatMessage) => boolean, next: (m: ChatMessage) => ChatMessage): ChatMessage | null {
    const arr = this.state.history[channel];
    const i = arr ? arr.findIndex(match) : -1;
    if (!arr || i < 0) return null;
    const updated = next(arr[i]);
    const copy = arr.slice();
    copy[i] = updated;
    this.state.history[channel] = copy;
    const p = updated.client_id ? this.pending[updated.client_id] : undefined;
    if (p && p.channel === channel) p.message = updated;
    return updated;
  }

  /** Whether `m` counts toward this user's unread badge (the server's rule:
   *  someone else's message, or a call this user missed). */
  private countsAsUnread(m: ChatMessage): boolean {
    const me = this.state.me?.id;
    if (m.user_id == null || m.user_id === me || m.type === "system") return false;
    return m.type !== "call" || callMissedBy(m.call, me);
  }

  /**
   * Merges a server snapshot (the newest page of a thread) into what this
   * client already holds: when the snapshot's oldest message is already
   * loaded, the older pages the user scrolled back to stay. Returns the new
   * array (pending sends included) and whether older history remains.
   */
  private mergeSnapshot(channel: string, incoming: HistoryMessage[], hasMore: boolean): { messages: ChatMessage[]; hasMore: boolean } {
    const held = (this.state.history[channel] || []).filter((m) => m._status !== "sending" && m._status !== "failed");
    let older: ChatMessage[] = [];
    let more = hasMore;
    if (incoming.length) {
      const i = held.findIndex((m) => m.id === incoming[0].id);
      if (i > 0) {
        older = held.slice(0, i);
        more = !!this.state.historyHasMore[channel];
      }
    }
    if (!older.length) this.historyEpoch[channel] = (this.historyEpoch[channel] || 0) + 1;
    return { messages: this.mergePending(channel, [...older, ...incoming]), hasMore: more };
  }

  /** Platform tells the store whether the UI is in the foreground. */
  setForeground(visible: boolean): void {
    // The server rings a hidden device through Web Push as well.
    if (visible !== this.foreground) this.send({ type: "visibility", visible });
    // Back in front: check at once that the socket still works (a phone
    // that slept or changed networks often holds a dead connection).
    if (visible && !this.foreground) this.socket?.wake();
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
          // The socket just closed; a call's signals wait for the resume.
          this.calls.linkDown();
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
    // Call signals (ICE candidates by the dozen) would flood the debug console.
    if (payload.type !== "call_signal") {
      this.debug("debug", "ws_send", payload.type, "channel" in payload ? { channel: payload.channel } : undefined);
    }
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
      // Call entries carry no delivery state.
      if (arr[i].type === "system" || arr[i].type === "call") continue;
      lastOwn = arr[i].user_id === this.state.me.id ? arr[i] : null;
      break;
    }
    if (!lastOwn || lastOwn._status === "failed") return null;
    if (lastOwn._status === "sending") return {messageId: lastOwn.id, label: "Sending…"};
    const read = lastOwn.position != null && st.peerLastReadPosition != null
      ? lastOwn.position <= st.peerLastReadPosition
      : !!st.peerLastReadAt && (lastOwn.created_at || 0) <= st.peerLastReadAt;
    if (read) {
      return { messageId: lastOwn.id, label: "Read" };
    }
    return { messageId: lastOwn.id, label: "Delivered" };
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
        this.applyDmRead(data.channel, data.user_id, data.last_read_at, data.last_read_position);
        break;
      case "dm_opened":
        this.applyDmOpened(data);
        break;
      case "call_outgoing":
      case "call_incoming":
      case "call_ringing":
      case "call_accepted":
      case "call_signal":
      case "call_resumed":
      case "call_ended":
      case "call_error":
        this.calls.handle(data);
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
    const previousChannel = s.activeChannel;
    const history: Record<string, ChatMessage[]> = {};
    const hasMore: Record<string, boolean> = {};
    for (const [ch, messages] of Object.entries(data.history || {})) {
      const merged = this.mergeSnapshot(ch, messages, !!data.history_has_more?.[ch]);
      history[ch] = merged.messages;
      hasMore[ch] = merged.hasMore;
    }
    for (const ch of Object.keys(s.history)) {
      if (!history[ch]) this.historyEpoch[ch] = (this.historyEpoch[ch] || 0) + 1;
    }
    s.history = history;
    for (const pending of Object.values(this.pending)) {
      if (!s.history[pending.channel]) s.history[pending.channel] = this.mergePending(pending.channel, []);
      if (!s.dmThreads.some((t) => t.channel === pending.channel)) {
        const peer = this.peerOf(pending.channel);
        if (peer != null) s.dmThreads.push({channel: pending.channel, peer_id: peer});
      }
    }
    s.historyHasMore = hasMore;
    s.historyLoading = {};
    s.pageSize = data.page_size || PAGE_SIZE_DEFAULT;
    s.maxUpload = data.max_upload || s.maxUpload;
    s.maxAttachments = data.max_attachments || MAX_ATTACHMENTS_DEFAULT;
    s.dmState = {};
    for (const [ch, st] of Object.entries(data.dm_state || {})) {
      s.dmState[ch] = dmStateFromPayload(st);
    }
    const saved = this.readLastChannel();
    s.activeChannel = this.channelExists(previousChannel) ? previousChannel : this.channelExists(saved) ? saved : null;
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
    // Every socket starts out "visible" on the server.
    if (!this.foreground) this.send({ type: "visibility", visible: false });
    this.calls.linkReady();
  }

  /** Keep failed/sending messages across server snapshots, reconciling saved nonces. */
  private mergePending(channel: string, incoming: HistoryMessage[]): ChatMessage[] {
    const messages = incoming.slice() as ChatMessage[];
    for (const [cid, p] of Object.entries(this.pending)) {
      if (p.channel !== channel) continue;
      const index = messages.findIndex((m) => m.client_id === cid && m.user_id === this.state.me?.id);
      if (index >= 0) messages[index] = this.reconcilePending(cid, messages[index]) ?? messages[index];
      else messages.push(p.message);
    }
    return messages;
  }

  /** Confirms a pending send with the server's copy; returns the new
   *  message object (the caller puts it in the history array). */
  private reconcilePending(cid: string, message: HistoryMessage): ChatMessage | null {
    const p = this.pending[cid];
    if (!p) return null;
    if (p.timer) clearTimeout(p.timer);
    delete this.pending[cid];
    const confirmed: ChatMessage = { ...p.message, ...message, client_id: cid, _status: "delivered" };
    if (p.edit && p.edit !== message.text) {
      confirmed.text = p.edit;
      confirmed.edited_at = Math.floor(Date.now() / 1000);
      this.send({type: "edit", id: message.id, text: p.edit});
    }
    return confirmed;
  }

  private applyMessage(channel: string, msg: BroadcastMessage): void {
    const s = this.state;
    if (msg.author && msg.author.id != null) {
      s.users[msg.author.id] = { ...(s.users[msg.author.id] || ({} as PublicUser)), ...msg.author };
    }

    // Reconcile our own optimistic bubble (matched by the echoed client_id).
    if (msg.client_id && this.pending[msg.client_id] && s.me && msg.user_id === s.me.id) {
      const cid = msg.client_id;
      const confirmed = this.reconcilePending(cid, msg)!;
      const arr = s.history[channel] || [];
      let i = arr.findIndex((m) => m.client_id === cid);
      if (i < 0) i = arr.findIndex((m) => m.id === msg.id);
      const copy = arr.slice();
      if (i >= 0) copy[i] = confirmed;
      else copy.push(confirmed);
      s.history[channel] = copy;
      this.emit();
      return;
    }

    // A message proves its author's socket is up — except a call entry, which
    // the server writes for the caller, possibly after their socket closed.
    if (msg.type !== "call" && msg.user_id != null && !s.online.has(msg.user_id)) {
      s.online.add(msg.user_id);
    }
    const arr = s.history[channel] || [];
    if (arr.some((m) => m.id === msg.id)) return;
    s.history[channel] = [...arr, msg as ChatMessage];

    if (isDM(channel) && !s.dmThreads.find((t) => t.channel === channel)) {
      const peer = this.peerOf(channel);
      if (peer != null) s.dmThreads.push({ channel, peer_id: peer });
    }

    let notify: NotifyEvent | null = null;
    // An answered or declined call is no news to the callee (as on the server).
    if (isDM(channel) && s.me && this.countsAsUnread(msg as ChatMessage)) {
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
          kind: msg.type === "call" ? "missed_call" : "message",
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
    if (this.updateMessage(channel, (x) => x.id === id, (x) => ({ ...x, text, edited_at: editedAt }))) this.emit();
  }

  private applyProfileUpdate(p: PublicUser): void {
    const s = this.state;
    const isSelf = s.me && p.id === s.me.id;
    if (!isSelf && !s.users[p.id]) return; // ignore strangers
    s.users[p.id] = { ...(s.users[p.id] || ({} as PublicUser)), ...p };
    if (isSelf) s.me = { ...s.me, ...p };
    this.emit();
  }

  private applyDmRead(channel: string, userId: number, lastReadAt: number, position?: number): void {
    const st = this.dmStateFor(channel);
    if (this.state.me && userId === this.state.me.id) {
      this.state.dmState[channel] = { ...st, lastReadAt: lastReadAt || st.lastReadAt, unreadCount: 0 };
    } else {
      // A receipt only moves forward (two tabs of the peer may answer out of order).
      this.state.dmState[channel] = {
        ...st,
        peerLastReadAt: Math.max(lastReadAt || 0, st.peerLastReadAt || 0),
        peerLastReadPosition: position != null && st.peerLastReadPosition != null ? Math.max(position, st.peerLastReadPosition) : (position ?? st.peerLastReadPosition),
      };
    }
    this.emit();
  }

  private applyDmOpened(data: Extract<ServerEvent, { type: "dm_opened" }>): void {
    const s = this.state;
    const merged = this.mergeSnapshot(data.channel, data.history || [], !!data.has_more);
    s.history[data.channel] = merged.messages;
    s.historyHasMore[data.channel] = merged.hasMore;
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
    // A reply and staged files belong to the conversation they were made in.
    this.state.replyTo = null;
    this.state.pendingAtt = [];
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

  /** Whether another file may be staged for the current message. */
  canAttachMore(): boolean {
    return this.state.pendingAtt.length < this.state.maxAttachments;
  }

  addPendingAtt(a: Attachment): void {
    if (!this.canAttachMore()) {
      this.toast(`A message can carry at most ${this.state.maxAttachments} files`, true);
      return;
    }
    this.state.pendingAtt = [...this.state.pendingAtt, a];
    this.emit();
  }

  removePendingAtt(index: number): void {
    const atts = [...this.state.pendingAtt];
    atts.splice(index, 1);
    this.state.pendingAtt = atts;
    this.emit();
  }

  /**
   * Uploads a file. By default it is staged as a pending attachment of the
   * conversation that was open when the upload started (and dropped if the
   * user has left that conversation by the time it finishes). With
   * `{ stage: false }` it is only returned, for a caller that sends it itself.
   */
  async uploadAttachment(file: Parameters<ApiClient["upload"]>[0], name: string): Promise<boolean>;
  async uploadAttachment(file: Parameters<ApiClient["upload"]>[0], name: string, opts: { stage: false }): Promise<Attachment | null>;
  async uploadAttachment(file: Parameters<ApiClient["upload"]>[0], name: string, opts?: { stage: false }): Promise<boolean | Attachment | null> {
    const channel = this.state.activeChannel;
    if (opts?.stage !== false && !this.canAttachMore()) {
      this.toast(`A message can carry at most ${this.state.maxAttachments} files`, true);
      return false;
    }
    try {
      const j = await this.api.upload(file);
      const att: Attachment = {
        name: j.name || name,
        url: j.url,
        size: j.size,
        mime: j.mime,
        width: j.width || 0,
        height: j.height || 0,
      };
      if (opts?.stage === false) return att;
      if (this.state.activeChannel !== channel) return false;
      this.addPendingAtt(att);
      return true;
    } catch {
      this.toast(`Upload failed: ${name}`, true);
      return opts?.stage === false ? null : false;
    }
  }

  /**
   * Sends a message optimistically. Without `attachments` it sends the
   * composer's staged files (and clears them); with `attachments` it sends
   * exactly those and leaves the staged files alone. `channel` defaults to
   * the open conversation; a caller that started work in one conversation
   * (a voice message, a dictation) passes that conversation so the result
   * never lands in another one. The open reply applies only to its own
   * conversation. Returns null when nothing was sent.
   */
  sendMessage(text: string, attachments?: Attachment[], channel: string | null = this.state.activeChannel): { clientId: string } | null {
    const s = this.state;
    if (!channel || !s.me || !this.channelExists(channel)) return null;
    const trimmed = text.trim();
    const staged = attachments == null;
    const atts = (attachments ?? (channel === s.activeChannel ? s.pendingAtt : [])).slice(0, s.maxAttachments);
    if (!trimmed && !atts.length) return null;
    const reply = s.replyTo && s.replyTo.channel === channel ? s.replyTo : null;
    const cid = "c" + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
    const optimistic: ChatMessage = {
      id: cid,
      client_id: cid,
      type: "message",
      channel,
      user_id: s.me.id,
      text: trimmed,
      reply_to: reply ? reply.id : null,
      attachments: atts,
      created_at: Math.floor(Date.now() / 1000),
      edited_at: null,
      _status: "sending",
    };
    s.history[channel] = [...(s.history[channel] || []), optimistic];
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
    this.pending[cid] = { channel, message: optimistic, timer: setTimeout(() => this.markSendFailed(channel, cid), SEND_TIMEOUT_MS) };
    if (staged && channel === s.activeChannel) s.pendingAtt = [];
    if (reply) s.replyTo = null;
    this.emit();
    return { clientId: cid };
  }

  private findOptimistic(channel: string, cid: string): ChatMessage | undefined {
    return (this.state.history[channel] || []).find((m) => m.client_id === cid) ?? this.pending[cid]?.message;
  }

  private markSendFailed(channel: string, cid: string): void {
    const p = this.pending[cid];
    if (p?.timer) clearTimeout(p.timer);
    if (p) p.timer = null;
    if (this.updateMessage(channel, (m) => m.client_id === cid && m._status === "sending", (m) => ({ ...m, _status: "failed" }))) this.emit();
  }

  /** Re-sends a failed optimistic message with the same client_id nonce. */
  retrySend(cid: string): void {
    const p = this.pending[cid];
    const channel = p ? p.channel : this.state.activeChannel;
    if (!channel) return;
    const found = this.findOptimistic(channel, cid);
    // Only a failed send is retried; a confirmed one is already delivered.
    if (!found || found._status !== "failed") return;
    const m = this.updateMessage(channel, (x) => x.client_id === cid, (x) => ({ ...x, _status: "sending" })) ?? { ...found, _status: "sending" as const };
    this.send({
      type: "message",
      channel,
      text: m.text,
      reply_to: m.reply_to,
      attachments: m.attachments,
      client_id: cid,
    });
    if (p?.timer) clearTimeout(p.timer);
    this.pending[cid] = { ...p, channel, message: m, timer: setTimeout(() => this.markSendFailed(channel, cid), SEND_TIMEOUT_MS) };
    this.emit();
  }

  /** Edits one of our own messages (optimistic local apply + server edit). */
  editMessage(m: ChatMessage, text: string): void {
    const trimmed = text.trim();
    if (!trimmed || trimmed === m.text) return;
    const key = messageKey(m);
    const pending = m.client_id ? this.pending[m.client_id] : undefined;
    const updated = this.updateMessage(m.channel, (x) => messageKey(x) === key, (x) => ({ ...x, text: trimmed, edited_at: Math.floor(Date.now() / 1000) }));
    if (!updated) return;
    if (pending) pending.edit = trimmed;
    else this.send({ type: "edit", id: updated.id, text: trimmed });
    this.emit();
  }

  /** Lazily loads the previous page of history for a channel. */
  async loadOlder(channel: string): Promise<void> {
    const s = this.state;
    if (!channel || s.historyLoading[channel] || !s.historyHasMore[channel]) return;
    const arr = s.history[channel] || [];
    const first = arr.find((m) => m._status !== "sending" && m._status !== "failed");
    const oldest = first?.created_at ?? null;
    if (oldest == null) return;
    const epoch = this.historyEpoch[channel] || 0;
    s.historyLoading[channel] = true;
    this.emit();
    try {
      const j = await this.api.history(channel, oldest, s.pageSize, first?.id);
      // A snapshot or a delete replaced the thread meanwhile: this page no
      // longer joins onto what is held.
      if ((this.historyEpoch[channel] || 0) !== epoch) return;
      const older = (j.messages || []) as ChatMessage[];
      const cur = s.history[channel] || [];
      if (!older.length) {
        s.historyHasMore[channel] = false;
      } else {
        const seen = new Set(cur.map((m) => m.id));
        s.history[channel] = [...older.filter((m) => !seen.has(m.id)), ...cur];
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
    const held = (this.state.history[channel] || []).filter((m) => m.position != null);
    const newest = held[held.length - 1];
    try {
      const j = await this.api.markDMRead(channel, newest?.position);
      // Messages that arrived after the request was made stay unread.
      const cur = this.state.history[channel] || [];
      const cut = newest ? cur.findIndex((m) => m.id === newest.id) : -1;
      const later = cut >= 0 ? cur.slice(cut + 1).filter((m) => this.countsAsUnread(m)).length : 0;
      const reading = channel === this.state.activeChannel && this.foreground;
      const st = this.dmStateFor(channel);
      this.state.dmState[channel] = {
        ...st,
        lastReadAt: j.last_read_at || Math.floor(Date.now() / 1000),
        unreadCount: reading ? 0 : later,
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
      // As on the server: a thread marked unread counts one unread message.
      this.state.dmState[channel] = { ...st, unreadCount: Math.max(st.unreadCount, 1) };
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
      this.historyEpoch[channel] = (this.historyEpoch[channel] || 0) + 1;
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

  /** Push-received hook (web service worker): bumps unread eagerly — only
   *  while the socket is down, since a live socket delivers the message
   *  itself and already counted it. */
  handlePushReceived(channel: string): void {
    if (this.state.connState === "online") return;
    if (isDM(channel) && this.peerOf(channel) != null && channel !== this.state.activeChannel) {
      const st = this.dmStateFor(channel);
      this.state.dmState[channel] = { ...st, unreadCount: (st.unreadCount || 0) + 1 };
      this.emit();
    }
  }

  setPushSubscribed(on: boolean): void {
    if (this.state.pushSubscribed === on) return;
    this.state.pushSubscribed = on;
    this.emit();
  }

  /** Channel-open request coming from an external surface (push tap, deep
   *  link). A channel this user is not part of (a notification left over
   *  from another account) is ignored. */
  openChannelExternal(channel: string): void {
    if (this.peerOf(channel) == null) return;
    if (isDM(channel)) {
      const peer = this.peerOf(channel);
      if (peer != null && !this.state.dmThreads.find((t) => t.channel === channel)) {
        this.state.dmThreads.push({ channel, peer_id: peer });
      }
    }
    this.switchChannel(channel);
  }

  /** Calls a DM peer (see CallManager.start). */
  startCall(peerId: number, media: CallMedia): void {
    const peer = this.userFor(peerId);
    if (peer) void this.calls.start(peer, media);
  }

  /** Full reset — used on logout. */
  reset(): void {
    for (const p of Object.values(this.pending)) {
      if (p.timer) clearTimeout(p.timer);
    }
    this.pending = {};
    this.historyEpoch = {};
    this.calls.destroy();
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
