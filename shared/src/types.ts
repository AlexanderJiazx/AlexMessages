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
  /** Authenticated URL under /uploads/<uid>/. */
  url: string;
  size: number;
  mime: string;
  width: number;
  height: number;
}

/** Message shape used by init/history payloads — no embedded author. */
export interface HistoryMessage {
  id: string;
  /** "message" for normal chat; "call" for a finished call's thread entry;
   *  "system" for legacy join/leave rows. */
  type: string;
  channel: string;
  user_id: number | null;
  text: string;
  reply_to: string | null;
  created_at: number;
  /** Server ordering cursor; display time remains in seconds. */
  position?: number;
  client_id?: string;
  edited_at: number | null;
  attachments: Attachment[];
  /** Set on type="call" entries only. */
  call?: CallRecord;
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
  last_read_position?: number;
  peer_last_read_position?: number;
}

/** Per-user DM flags in client-side camelCase. */
export interface DmState {
  pinned: boolean;
  lastReadAt: number;
  clearedAt: number;
  unreadCount: number;
  peerLastReadAt: number;
  peerLastReadPosition?: number;
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

// ---------- calls ----------

export type CallMedia = "audio" | "video";

/** How a finished call ended (calls.status on the server). "failed" was
 *  answered but its media never connected. The callee reads every outcome
 *  but completed, declined, and failed as a missed call. */
export type CallStatus = "completed" | "declined" | "failed" | "cancelled" | "no_answer" | "unavailable" | "busy";

/** A finished call, carried by its type="call" thread entry. Times are unix
 *  seconds; duration is 0 when the call was never answered. */
export interface CallRecord {
  id: number;
  media: CallMedia;
  status: CallStatus | string;
  caller_id: number | null;
  callee_id: number | null;
  started_at: number;
  answered_at: number | null;
  ended_at: number;
  duration: number;
}

/** One RTCIceServer entry, as the server hands it out per call. */
export interface IceServer {
  urls: string[];
  username?: string;
  credential?: string;
}

/** A live call as one participant sees it (internal/webapp/calls.go). */
export interface CallInfo {
  id: string;
  channel: string;
  caller_id: number;
  callee_id: number;
  media: CallMedia;
  state: "ringing" | "active";
  started_at: number;
  answered_at: number | null;
  /** The other participant. */
  peer: PublicUser;
  ice_servers: IceServer[];
}

/** Why a live call ended for this device: a CallStatus when the call itself
 *  ended, or a device-level notice — "gone" (the call no longer includes
 *  this device) and "answered_elsewhere" (another device of yours took it). */
export type CallEndReason = CallStatus | "gone" | "answered_elsewhere";

/** Opaque peer-to-peer payloads the server relays between the two devices
 *  on a call. Only the caller sends offers; "restart" asks it for an ICE
 *  restart, and "state" shares the sender's mic/camera switches. */
export type CallSignal =
  | { kind: "offer"; sdp: string }
  | { kind: "answer"; sdp: string }
  | { kind: "candidate"; candidate: string; sdpMid: string | null; sdpMLineIndex: number | null }
  | { kind: "state"; mic: boolean; camera: boolean }
  | { kind: "restart" };

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
  | { type: "dm_read"; channel: string; user_id: number; last_read_at: number; last_read_position?: number }
  | { type: "call_outgoing"; client_call_id: string; call: CallInfo }
  | { type: "call_incoming"; call: CallInfo }
  | { type: "call_ringing"; call_id: string }
  | { type: "call_accepted"; client_call_id: string; call: CallInfo }
  | { type: "call_signal"; call_id: string; payload: CallSignal }
  | { type: "call_resumed"; call: CallInfo }
  | { type: "call_ended"; call_id: string; client_call_id?: string; reason: CallEndReason }
  | { type: "call_error"; client_call_id: string; error: "invalid" | "busy_self" }
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
  | { type: "call_start"; peer_id: number; media: CallMedia; client_call_id: string; device: string }
  | { type: "call_accept"; call_id: string; device: string }
  /** reason "failed": the call was answered but its media never connected. */
  | { type: "call_hangup"; call_id: string; reason?: "failed" }
  | { type: "call_signal"; call_id: string; payload: CallSignal }
  | { type: "call_resume"; call_id: string; device: string }
  /** Whether this device's screen shows the app; calls ring through Web Push
   *  when none of the callee's devices does. */
  | { type: "visibility"; visible: boolean }
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
