/**
 * Wire models for the Alex Messages protocol.
 *
 * These mirror the Go backend's JSON shapes exactly (snake_case fields —
 * see internal/webapp/ws.go and internal/db/db.go). Adding a field server-side
 * is safe; renaming or retyping one breaks the contract.
 */

/** Public-facing user record (no password hash, no internals). */
export interface PublicUser {
  id: number;
  username: string;
  display_name: string;
  bio: string;
  /** Public avatar URL under /avatars/, or "" when the user has no photo. */
  avatar: string;
}

/** File attached to a message. `width`/`height` are 0 when unknown. */
export interface Attachment {
  name: string;
  /** Public URL under /uploads/<uid>/. */
  url: string;
  size: number;
  mime: string;
  width: number;
  height: number;
}

/** Message shape used by init/history payloads — no embedded author. */
export interface HistoryMessage {
  id: string;
  /** "message" for normal chat; "system" for legacy join/leave rows. */
  type: string;
  channel: string;
  user_id: number | null;
  text: string;
  reply_to: string | null;
  created_at: number;
  edited_at: number | null;
  attachments: Attachment[];
}

/** Live "message" broadcast shape — carries the resolved author and the
 *  echoed client_id nonce used to reconcile optimistic sends. */
export interface BroadcastMessage extends HistoryMessage {
  author?: PublicUser | null;
  client_id?: string;
}

/** Per-user DM flags as sent over the wire (snake_case). */
export interface DmStatePayload {
  pinned: boolean;
  last_read_at: number;
  cleared_at: number;
  force_unread: boolean;
  unread_count: number;
  peer_last_read_at: number;
}

/** Per-user DM flags in client-side camelCase. */
export interface DmState {
  pinned: boolean;
  lastReadAt: number;
  clearedAt: number;
  unreadCount: number;
  peerLastReadAt: number;
}

export const EMPTY_DM_STATE: DmState = {
  pinned: false,
  lastReadAt: 0,
  clearedAt: 0,
  unreadCount: 0,
  peerLastReadAt: 0,
};

/** One sidebar thread: the DM channel plus the peer it belongs to. */
export interface DmThreadRef {
  channel: string;
  peer_id: number;
}

/** The first frame the server sends on a fresh /ws connection. */
export interface InitPayload {
  type: "init";
  me: PublicUser;
  users: PublicUser[];
  contacts: number[];
  online: number[];
  dm_threads: DmThreadRef[];
  dm_state: Record<string, DmStatePayload>;
  history: Record<string, HistoryMessage[]>;
  history_has_more: Record<string, boolean>;
  page_size: number;
  max_upload: number;
}

// ---------- server → client events ----------

export type ServerEvent =
  | InitPayload
  | { type: "message"; channel: string; message: BroadcastMessage }
  | { type: "message_edited"; channel: string; id: string; text: string; edited_at: number }
  | { type: "presence"; online: number[] }
  | { type: "profile_update"; profile: PublicUser }
  | {
      type: "dm_opened";
      channel: string;
      peer_id: number;
      history: HistoryMessage[];
      has_more: boolean;
      state: DmStatePayload;
    }
  | { type: "dm_read"; channel: string; user_id: number; last_read_at: number }
  | { type: "pong" };

// ---------- client → server messages ----------

export type ClientMessage =
  | {
      type: "message";
      channel: string;
      text: string;
      reply_to?: string | null;
      attachments?: Attachment[];
      client_id?: string;
    }
  | { type: "edit"; id: string; text: string }
  | { type: "switch"; channel: string }
  | { type: "open_dm"; peer_id: number }
  | { type: "ping" };

// ---------- REST response shapes ----------

export interface MeResponse {
  user: PublicUser;
  is_admin: boolean;
  created_at: number;
}

export interface LoginResponse {
  ok: boolean;
  user: PublicUser;
  /** Bearer token for cookie-less clients (React Native). Absent in legacy
   *  servers — clients must tolerate it being undefined. */
  session_token?: string;
}

export interface HistoryResponse {
  channel: string;
  messages: HistoryMessage[];
  has_more: boolean;
}

export interface LinkPreview {
  url: string;
  title?: string;
  description?: string;
  image?: string;
  site_name?: string;
}
