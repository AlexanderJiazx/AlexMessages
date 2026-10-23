/**
 * CallManager tests: a fake WebRTC platform stands in for the browser /
 * react-native-webrtc, and a MockSocket for /ws, so each flow can be driven
 * frame by frame and its sends inspected.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../src/api";
import {
  CALL_CONNECT_TIMEOUT_MS,
  CALL_ENDED_LINGER_MS,
  CALL_ICE_RESTART_DELAY_MS,
  CALL_RECONNECT_TIMEOUT_MS,
  type CallIceCandidate,
  type CallMediaConstraints,
  type CallPeerConnection,
  type CallPeerEvent,
  type CallPlatform,
  type CallStream,
  type CallTrack,
} from "../src/call";
import { callEntryText, callOutcomeText, callStatusText, fmtCallDuration, lastMessagePreviewFor } from "../src/format";
import type { SocketLike, WebSocketFactory } from "../src/socket";
import { ChatStore } from "../src/store";
import type { BroadcastMessage, CallInfo, CallRecord, HistoryMessage, ServerEvent } from "../src/types";

// ---------- fake WebRTC ----------

class FakeTrack implements CallTrack {
  enabled = true;
  stopped = false;
  constructor(readonly kind: string, readonly label = kind) {}
  stop(): void {
    this.stopped = true;
  }
}

class FakeStream implements CallStream {
  constructor(readonly tracks: CallTrack[]) {}
  getTracks(): CallTrack[] {
    return this.tracks.slice();
  }
  getAudioTracks(): CallTrack[] {
    return this.tracks.filter((t) => t.kind === "audio");
  }
  getVideoTracks(): CallTrack[] {
    return this.tracks.filter((t) => t.kind === "video");
  }
}

class FakeTransceiver {
  sender: { track: CallTrack | null; replaceTrack: (t: CallTrack | null) => Promise<void> };
  receiver: { track: CallTrack };
  constructor(kind: string, track: CallTrack | null, public direction: string) {
    const sender = {
      track,
      replaceTrack: async (t: CallTrack | null) => {
        sender.track = t;
      },
    };
    this.sender = sender;
    this.receiver = { track: new FakeTrack(kind, "remote-" + kind) };
  }
}

class FakePC implements CallPeerConnection {
  remoteDescription: { type: string; sdp?: string } | null = null;
  localDescription: { type: string; sdp?: string } | null = null;
  iceConnectionState = "new";
  transceivers: FakeTransceiver[] = [];
  candidates: CallIceCandidate[] = [];
  offers = 0;
  closed = false;
  private listeners = new Map<string, ((ev: CallPeerEvent) => void)[]>();

  constructor(readonly config: { iceServers: unknown[] }) {}

  addTransceiver(trackOrKind: CallTrack | "audio" | "video", init: { direction: string }): FakeTransceiver {
    const track = typeof trackOrKind === "string" ? null : trackOrKind;
    const tx = new FakeTransceiver(track ? track.kind : (trackOrKind as string), track, init.direction);
    this.transceivers.push(tx);
    return tx;
  }
  getTransceivers(): FakeTransceiver[] {
    return this.transceivers;
  }
  async createOffer(options?: { iceRestart?: boolean }) {
    this.offers++;
    return { type: "offer", sdp: `offer-${this.offers}${options?.iceRestart ? "-restart" : ""}` };
  }
  async createAnswer() {
    return { type: "answer", sdp: "answer" };
  }
  async setLocalDescription(d: { type: string; sdp?: string }) {
    this.localDescription = d;
  }
  async setRemoteDescription(d: { type: string; sdp?: string }) {
    this.remoteDescription = d;
    // A remote offer creates the receiving side's transceivers, recvonly.
    if (d.type === "offer" && !this.transceivers.length) {
      this.transceivers.push(new FakeTransceiver("audio", null, "recvonly"), new FakeTransceiver("video", null, "recvonly"));
    }
  }
  async addIceCandidate(c: CallIceCandidate) {
    this.candidates.push(c);
  }
  addEventListener(type: string, fn: (ev: CallPeerEvent) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) || []), fn]);
  }
  fire(type: string, ev: CallPeerEvent = {}): void {
    for (const fn of this.listeners.get(type) || []) fn(ev);
  }
  setIce(state: string): void {
    this.iceConnectionState = state;
    this.fire("iceconnectionstatechange");
  }
  close(): void {
    this.closed = true;
  }
}

function fakePlatform(opts: { failVideo?: string; failAll?: string } = {}) {
  const pcs: FakePC[] = [];
  const getUserMedia = vi.fn(async (c: CallMediaConstraints): Promise<CallStream> => {
    if (opts.failAll) throw Object.assign(new Error("denied"), { name: opts.failAll });
    if (c.video && opts.failVideo) throw Object.assign(new Error("no camera"), { name: opts.failVideo });
    const tracks: CallTrack[] = [];
    if (c.audio) tracks.push(new FakeTrack("audio", "mic"));
    if (c.video) tracks.push(new FakeTrack("video", "camera-" + c.video.facingMode));
    return new FakeStream(tracks);
  });
  const platform: CallPlatform = {
    createPeerConnection: (config) => {
      const pc = new FakePC(config);
      pcs.push(pc);
      return pc;
    },
    getUserMedia,
    createStream: (tracks) => new FakeStream(tracks),
  };
  return { platform, pcs, getUserMedia };
}

// ---------- fake /ws ----------

class MockSocket implements SocketLike {
  sent: string[] = [];
  private listeners = new Map<string, ((ev: { data?: string; code?: number }) => void)[]>();
  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.fire("close", { code: 1000 });
  }
  addEventListener(type: string, listener: (ev: { data?: string; code?: number }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) || []), listener]);
  }
  fire(type: string, ev: { data?: string; code?: number } = {}): void {
    for (const l of this.listeners.get(type) || []) l(ev);
  }
  event(data: ServerEvent): void {
    this.fire("message", { data: JSON.stringify(data) });
  }
  frames(type?: string): Record<string, unknown>[] {
    return this.sent.map((s) => JSON.parse(s)).filter((f) => !type || f.type === type);
  }
}

const me = { id: 1, username: "alice", display_name: "Alice", bio: "", avatar: "" };
const bob = { id: 2, username: "bob", display_name: "Bob", bio: "", avatar: "" };
const carol = { id: 3, username: "carol", display_name: "Carol", bio: "", avatar: "" };

const initFrame = (): ServerEvent => ({
  type: "init",
  me,
  users: [me, bob],
  contacts: [],
  online: [2],
  dm_threads: [{ channel: "dm:1:2", peer_id: 2 }],
  dm_state: {},
  history: { "dm:1:2": [] },
  history_has_more: {},
  page_size: 50,
  max_upload: 1,
});

/** Bob calling Alice, or (outgoing) Alice calling Bob, as the server sends it. */
const call = (over: Partial<CallInfo> = {}): CallInfo => ({
  id: "call1",
  channel: "dm:1:2",
  caller_id: 2,
  callee_id: 1,
  media: "video",
  state: "ringing",
  started_at: 1000,
  answered_at: null,
  peer: bob,
  ice_servers: [{ urls: ["stun:stun.example:3478"] }],
  ...over,
});
const outgoing = (over: Partial<CallInfo> = {}) => call({ caller_id: 1, callee_id: 2, ...over });

function harness(platformOpts: Parameters<typeof fakePlatform>[0] = {}) {
  const sockets: MockSocket[] = [];
  const factory: WebSocketFactory = () => {
    const s = new MockSocket();
    sockets.push(s);
    return s;
  };
  const fake = fakePlatform(platformOpts);
  const notify = vi.fn();
  const toast = vi.fn();
  const store = new ChatStore({
    api: new ApiClient({ baseUrl: "http://test" }),
    wsUrl: () => "ws://test/ws",
    WebSocketImpl: factory,
    callPlatform: fake.platform,
    onNotify: notify,
    onToast: toast,
  });
  vi.spyOn(store["api"], "getMe").mockResolvedValue({ user: me, is_admin: false, created_at: 0 });
  store.connect();
  sockets[0].fire("open");
  sockets[0].event(initFrame());
  const ws = () => sockets[sockets.length - 1];
  const server = (ev: ServerEvent) => ws().event(ev);
  const signals = () => ws().frames("call_signal").map((f) => f.payload as Record<string, unknown>);
  return { store, sockets, ws, server, signals, notify, toast, ...fake };
}

const settle = () => vi.advanceTimersByTimeAsync(0);
const last = <T>(arr: T[]): T => arr[arr.length - 1];

/** Alice calls Bob, and Bob answers: returns once the offer is out. */
async function connectedOutgoing(h: ReturnType<typeof harness>) {
  h.store.startCall(2, "video");
  await settle();
  const start = h.ws().frames("call_start")[0];
  const nonce = start.client_call_id as string;
  h.server({ type: "call_outgoing", client_call_id: nonce, call: outgoing() });
  h.server({ type: "call_accepted", client_call_id: nonce, call: outgoing({ state: "active", answered_at: 1001 }) });
  await settle();
  return { nonce, pc: h.pcs[0] };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("outgoing calls", () => {
  it("captures, rings, offers on accept, and hangs up", async () => {
    const h = harness();
    h.store.startCall(2, "video");
    await settle();
    expect(h.getUserMedia).toHaveBeenCalledWith({ audio: true, video: { facingMode: "user" } });
    const start = h.ws().frames("call_start")[0];
    expect(start).toMatchObject({ peer_id: 2, media: "video", device: h.store.calls.device });
    expect(h.store.state.call).toMatchObject({ phase: "outgoing", direction: "outgoing", ringing: false, cameraOn: true });
    expect(h.store.state.call!.localStream!.getTracks()).toHaveLength(2);

    const nonce = start.client_call_id as string;
    h.server({ type: "call_outgoing", client_call_id: nonce, call: outgoing() });
    expect(h.store.state.call!.id).toBe("call1");
    h.server({ type: "call_ringing", call_id: "call1" });
    expect(h.store.state.call!.ringing).toBe(true);
    expect(callStatusText(h.store.state.call!)).toBe("Ringing…");

    h.server({ type: "call_accepted", client_call_id: nonce, call: outgoing({ state: "active", answered_at: 1001 }) });
    await settle();
    const pc = h.pcs[0];
    expect(pc.config.iceServers).toEqual([{ urls: ["stun:stun.example:3478"] }]);
    expect(pc.transceivers.map((t) => [t.sender.track?.kind, t.direction])).toEqual([
      ["audio", "sendrecv"],
      ["video", "sendrecv"],
    ]);
    expect(h.signals()).toEqual([
      { kind: "state", mic: true, camera: true },
      { kind: "offer", sdp: "offer-1" },
    ]);
    expect(h.store.state.call!.phase).toBe("connecting");

    pc.fire("icecandidate", { candidate: { candidate: "candidate:1", sdpMid: "0", sdpMLineIndex: 0 } });
    expect(h.signals()[2]).toEqual({ kind: "candidate", candidate: "candidate:1", sdpMid: "0", sdpMLineIndex: 0 });

    h.server({ type: "call_signal", call_id: "call1", payload: { kind: "answer", sdp: "answer" } });
    h.server({ type: "call_signal", call_id: "call1", payload: { kind: "candidate", candidate: "candidate:9", sdpMid: "0", sdpMLineIndex: 0 } });
    await settle();
    expect(pc.remoteDescription).toEqual({ type: "answer", sdp: "answer" });
    expect(pc.candidates.map((c) => c.candidate)).toEqual(["candidate:9"]);

    pc.fire("track", { track: new FakeTrack("audio") });
    pc.fire("track", { track: new FakeTrack("video") });
    pc.setIce("connected");
    expect(h.store.state.call!.phase).toBe("active");
    expect(h.store.state.call!.remoteStream!.getTracks().map((t) => t.kind)).toEqual(["audio", "video"]);

    const tracks = h.store.state.call!.localStream!.getTracks() as FakeTrack[];
    h.store.calls.hangUp();
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "call1" }]);
    expect(tracks.every((t) => t.stopped)).toBe(true);
    expect(pc.closed).toBe(true);
    expect(h.store.state.call).toMatchObject({ phase: "ended", outcome: "hung_up", localStream: null });
    await vi.advanceTimersByTimeAsync(CALL_ENDED_LINGER_MS);
    expect(h.store.state.call).toBeNull();
  });

  it("goes ahead with the camera off when there is no camera", async () => {
    const h = harness({ failVideo: "NotFoundError" });
    h.store.startCall(2, "video");
    await settle();
    expect(h.getUserMedia).toHaveBeenLastCalledWith({ audio: true, video: false });
    expect(h.ws().frames("call_start")[0]).toMatchObject({ media: "video" });
    expect(h.store.state.call).toMatchObject({ phase: "outgoing", cameraOn: false });
  });

  it("never rings anyone without a microphone", async () => {
    const h = harness({ failAll: "NotAllowedError" });
    h.store.startCall(2, "video");
    await settle();
    expect(h.getUserMedia).toHaveBeenCalledTimes(1); // a denial is not retried
    expect(h.ws().frames("call_start")).toEqual([]);
    expect(h.store.state.call).toMatchObject({
      phase: "ended",
      outcome: "no_media",
      error: "Allow camera and microphone access to call.",
    });
  });

  it("cancels a call hung up before the server named it", async () => {
    const h = harness();
    h.store.startCall(2, "audio");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    h.store.calls.hangUp();
    expect(h.ws().frames("call_hangup")).toEqual([]);
    h.server({ type: "call_outgoing", client_call_id: nonce, call: outgoing({ id: "late", media: "audio" }) });
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "late" }]);
  });

  it("reports why the peer couldn't take the call", async () => {
    const h = harness();
    h.store.startCall(2, "video");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    h.server({ type: "call_outgoing", client_call_id: nonce, call: outgoing() });
    h.server({ type: "call_ended", call_id: "call1", client_call_id: nonce, reason: "busy" });
    expect(h.store.state.call).toMatchObject({ phase: "ended", outcome: "busy" });
    expect(callStatusText(h.store.state.call!)).toBe("Busy");
    expect(h.ws().frames("call_hangup")).toEqual([]);
  });
});

describe("incoming calls", () => {
  it("rings, answers, and answers the offer", async () => {
    const h = harness();
    h.server({ type: "call_incoming", call: call() });
    expect(h.store.state.call).toMatchObject({ phase: "incoming", direction: "incoming", media: "video", id: "call1" });
    expect(h.getUserMedia).not.toHaveBeenCalled();

    void h.store.calls.accept();
    expect(h.store.state.call!.phase).toBe("connecting");
    await settle();
    expect(h.ws().frames("call_accept")).toEqual([{ type: "call_accept", call_id: "call1", device: h.store.calls.device }]);
    const pc = h.pcs[0];
    expect(pc.transceivers).toEqual([]); // the callee waits for the offer's

    h.server({ type: "call_accepted", client_call_id: "", call: call({ state: "active", answered_at: 1001 }) });
    // A candidate that beats the offer waits for it.
    h.server({ type: "call_signal", call_id: "call1", payload: { kind: "candidate", candidate: "candidate:9", sdpMid: "0", sdpMLineIndex: 0 } });
    h.server({ type: "call_signal", call_id: "call1", payload: { kind: "offer", sdp: "offer-1" } });
    await settle();
    expect(pc.remoteDescription).toEqual({ type: "offer", sdp: "offer-1" });
    expect(pc.candidates.map((c) => c.candidate)).toEqual(["candidate:9"]);
    expect(pc.transceivers.map((t) => [t.sender.track?.kind, t.direction])).toEqual([
      ["audio", "sendrecv"],
      ["video", "sendrecv"],
    ]);
    expect(h.signals()).toEqual([
      { kind: "state", mic: true, camera: true },
      { kind: "answer", sdp: "answer" },
    ]);
    expect(h.ws().frames("call_signal").some((f) => (f.payload as { kind: string }).kind === "offer")).toBe(false);
  });

  it("declining hangs up without touching the camera", () => {
    const h = harness();
    h.server({ type: "call_incoming", call: call() });
    h.store.calls.hangUp();
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "call1" }]);
    expect(h.getUserMedia).not.toHaveBeenCalled();
    expect(h.store.state.call!.phase).toBe("ended");
  });

  it("stops ringing when the caller gives up or another device answers", () => {
    const h = harness();
    h.server({ type: "call_incoming", call: call() });
    h.server({ type: "call_ended", call_id: "call1", reason: "cancelled" });
    expect(h.store.state.call!.outcome).toBe("cancelled");
    expect(callStatusText(h.store.state.call!)).toBe("Missed call");

    h.server({ type: "call_incoming", call: call({ id: "call2" }) });
    h.server({ type: "call_ended", call_id: "call2", reason: "answered_elsewhere" });
    expect(callStatusText(h.store.state.call!)).toBe("Answered on another device");
  });

  it("notifies when a call rings in the background", () => {
    const h = harness();
    h.store.setForeground(false);
    h.server({ type: "call_incoming", call: call({ media: "audio" }) });
    expect(h.notify).toHaveBeenCalledWith({ channel: "dm:1:2", title: "Bob", body: "Incoming voice call", kind: "call" });
  });

  it("calling the person who is calling you answers them", async () => {
    const h = harness();
    h.server({ type: "call_incoming", call: call() });
    h.store.startCall(2, "video");
    await settle();
    expect(h.ws().frames("call_start")).toEqual([]);
    expect(h.ws().frames("call_accept")).toHaveLength(1);
  });
});

describe("crossed calls", () => {
  it("joins the peer's call as its callee when both dial at once", async () => {
    const h = harness();
    h.store.startCall(2, "audio");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    // The server rang us for Bob's call before it saw ours...
    h.server({ type: "call_incoming", call: call() });
    expect(h.store.state.call).toMatchObject({ direction: "outgoing", phase: "outgoing" });
    // ...then answered ours by joining us to his.
    h.server({ type: "call_accepted", client_call_id: nonce, call: call({ state: "active", answered_at: 1001 }) });
    await settle();
    expect(h.store.state.call).toMatchObject({ id: "call1", phase: "connecting", media: "video" });
    h.server({ type: "call_signal", call_id: "call1", payload: { kind: "offer", sdp: "offer-1" } });
    await settle();
    expect(h.signals().map((p) => p.kind)).toEqual(["state", "answer"]);
  });

  it("hangs up the peer's call when we hung up before the server joined us to it", async () => {
    const h = harness();
    h.store.startCall(2, "audio");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    h.server({ type: "call_incoming", call: call() });
    h.store.calls.hangUp();
    expect(h.ws().frames("call_hangup")).toEqual([]);
    h.server({ type: "call_accepted", client_call_id: nonce, call: call({ state: "active", answered_at: 1001 }) });
    await settle();
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "call1" }]);
    expect(h.store.state.call!.phase).toBe("ended");
    expect(h.pcs).toEqual([]);
    // Bob's call, held back while ours was in flight, is forgotten with it:
    // a call placed next and refused doesn't bring it back.
    h.store.startCall(2, "audio");
    await settle();
    const next = h.ws().frames("call_start")[1].client_call_id as string;
    h.server({ type: "call_error", client_call_id: next, error: "busy_self" });
    expect(h.store.state.call).toMatchObject({ direction: "outgoing", phase: "ended", outcome: "busy_self" });
  });

  it("hangs up, and stops ringing, a crossed call that rang only after we hung up", async () => {
    const h = harness();
    h.store.startCall(2, "audio");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    h.store.calls.hangUp();
    // Bob's call rings here once ours is over...
    h.server({ type: "call_incoming", call: call() });
    expect(h.store.state.call).toMatchObject({ id: "call1", phase: "incoming" });
    // ...and the server then joins our call_start to it.
    h.server({ type: "call_accepted", client_call_id: nonce, call: call({ state: "active", answered_at: 1001 }) });
    await settle();
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "call1" }]);
    expect(h.store.state.call).toMatchObject({ id: "call1", phase: "ended", outcome: "hung_up" });
    expect(h.pcs).toEqual([]);
    // The server's end of that call changes nothing here.
    h.server({ type: "call_ended", call_id: "call1", reason: "completed" });
    expect(h.store.state.call!.outcome).toBe("hung_up");
  });

  it("shows the call that rang while our own call was refused", async () => {
    const h = harness();
    h.store.startCall(2, "video");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    h.server({ type: "call_incoming", call: call({ id: "call9", caller_id: 3, peer: carol, channel: "dm:1:3" }) });
    h.server({ type: "call_error", client_call_id: nonce, error: "busy_self" });
    expect(h.store.state.call).toMatchObject({ id: "call9", phase: "incoming", peer: carol });
  });
});

describe("reconnects", () => {
  it("holds signals until the server resumes the call, then restarts ICE", async () => {
    const h = harness();
    const { pc } = await connectedOutgoing(h);
    pc.setIce("connected");
    const before = h.signals().length;

    h.ws().close(); // the control socket drops
    pc.setIce("disconnected");
    expect(h.store.state.call!.phase).toBe("reconnecting");
    pc.fire("icecandidate", { candidate: { candidate: "candidate:2", sdpMid: "0", sdpMLineIndex: 0 } });
    h.store.calls.toggleMic();
    await vi.advanceTimersByTimeAsync(1500); // ChatSocket retries
    expect(h.sockets).toHaveLength(2);
    h.ws().fire("open");
    h.server(initFrame());
    expect(h.ws().frames("call_resume")).toEqual([{ type: "call_resume", call_id: "call1", device: h.store.calls.device }]);
    expect(h.signals()).toEqual([]);

    h.server({ type: "call_resumed", call: outgoing({ state: "active", answered_at: 1001 }) });
    await settle();
    expect(h.signals()).toEqual([
      { kind: "candidate", candidate: "candidate:2", sdpMid: "0", sdpMLineIndex: 0 },
      { kind: "state", mic: false, camera: true },
      { kind: "offer", sdp: "offer-2-restart" },
    ]);
    expect(before).toBeGreaterThan(0);
  });

  it("starts a call that was answered while the caller reconnected", async () => {
    const h = harness();
    h.store.startCall(2, "video");
    await settle();
    const nonce = h.ws().frames("call_start")[0].client_call_id as string;
    h.server({ type: "call_outgoing", client_call_id: nonce, call: outgoing() });
    h.ws().close();
    await vi.advanceTimersByTimeAsync(1500);
    h.ws().fire("open");
    h.server(initFrame());
    h.server({ type: "call_resumed", call: outgoing({ state: "active", answered_at: 1001 }) });
    await settle();
    expect(h.signals().map((p) => p.kind)).toEqual(["state", "offer"]);
  });

  it("drops a call the server no longer has", async () => {
    const h = harness();
    await connectedOutgoing(h);
    h.ws().close();
    await vi.advanceTimersByTimeAsync(1500);
    h.ws().fire("open");
    h.server(initFrame());
    h.server({ type: "call_ended", call_id: "call1", reason: "gone" });
    expect(h.store.state.call).toMatchObject({ phase: "ended", outcome: "gone" });
  });
});

describe("media trouble", () => {
  it("ends an answered call whose media never connects as failed", async () => {
    const h = harness();
    await connectedOutgoing(h);
    await vi.advanceTimersByTimeAsync(CALL_CONNECT_TIMEOUT_MS);
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "call1", reason: "failed" }]);
    expect(callStatusText(h.store.state.call!)).toBe("Couldn't connect");
  });

  it("restarts ICE after a while disconnected, then gives up", async () => {
    const h = harness();
    const { pc } = await connectedOutgoing(h);
    pc.setIce("connected");
    pc.setIce("disconnected");
    await vi.advanceTimersByTimeAsync(CALL_ICE_RESTART_DELAY_MS);
    expect(last(h.signals())).toEqual({ kind: "offer", sdp: "offer-2-restart" });
    await vi.advanceTimersByTimeAsync(CALL_RECONNECT_TIMEOUT_MS - CALL_ICE_RESTART_DELAY_MS);
    expect(h.ws().frames("call_hangup")).toEqual([{ type: "call_hangup", call_id: "call1" }]);
    expect(callStatusText(h.store.state.call!)).toBe("Connection lost");
  });

  it("asks the caller for an ICE restart when the callee's ICE fails", async () => {
    const h = harness();
    h.server({ type: "call_incoming", call: call() });
    void h.store.calls.accept();
    await settle();
    h.server({ type: "call_accepted", client_call_id: "", call: call({ state: "active", answered_at: 1001 }) });
    h.pcs[0].setIce("failed");
    expect(last(h.signals())).toEqual({ kind: "restart" });
  });
});

describe("in-call controls", () => {
  it("mutes, releases and recaptures the camera, and shows the peer's switches", async () => {
    const h = harness();
    const { pc } = await connectedOutgoing(h);
    const video = pc.transceivers[1];
    const firstCam = video.sender.track as FakeTrack;

    h.store.calls.toggleMic();
    expect(h.store.state.call!.micOn).toBe(false);
    expect((h.store.state.call!.localStream!.getAudioTracks()[0] as FakeTrack).enabled).toBe(false);

    await h.store.calls.toggleCamera();
    expect(video.sender.track).toBeNull();
    expect(firstCam.stopped).toBe(true);
    expect(h.store.state.call!.cameraOn).toBe(false);
    await h.store.calls.toggleCamera();
    expect(h.getUserMedia).toHaveBeenLastCalledWith({ audio: false, video: { facingMode: "user" } });
    expect(video.sender.track).not.toBeNull();
    expect(last(h.signals().filter((p) => p.kind === "state"))).toEqual({ kind: "state", mic: false, camera: true });

    await h.store.calls.switchCamera();
    expect((video.sender.track as FakeTrack).label).toBe("camera-environment");
    expect(h.store.state.call!.facing).toBe("environment");

    h.server({ type: "call_signal", call_id: "call1", payload: { kind: "state", mic: false, camera: false } });
    await settle();
    expect(h.store.state.call).toMatchObject({ peerMicOn: false, peerCameraOn: false });
  });

  it("frees each stream once the call stops showing it", async () => {
    const h = harness();
    const released = vi.fn();
    h.platform.releaseStream = released;
    const { pc } = await connectedOutgoing(h);
    const firstSelf = h.store.state.call!.localStream!;

    await h.store.calls.toggleCamera();
    expect(released.mock.calls).toEqual([[firstSelf]]);
    const secondSelf = h.store.state.call!.localStream!;
    pc.fire("track", { track: new FakeTrack("audio", "remote-audio") });
    const remoteAudio = h.store.state.call!.remoteStream!;
    pc.fire("track", { track: new FakeTrack("video", "remote-video") });
    const remoteBoth = h.store.state.call!.remoteStream!;
    expect(released.mock.calls).toEqual([[firstSelf], [remoteAudio]]);

    h.store.calls.hangUp();
    expect(released.mock.calls).toEqual([[firstSelf], [remoteAudio], [secondSelf], [remoteBoth]]);
    await vi.advanceTimersByTimeAsync(CALL_ENDED_LINGER_MS);
    expect(h.store.state.call).toBeNull();
    expect(released).toHaveBeenCalledTimes(4);
  });
});

describe("store integration", () => {
  it("reports visibility so hidden devices ring through Web Push too", async () => {
    const h = harness();
    h.store.setForeground(false);
    expect(h.ws().frames("visibility")).toEqual([{ type: "visibility", visible: false }]);
    h.ws().close();
    await vi.advanceTimersByTimeAsync(1500);
    h.ws().fire("open");
    h.server(initFrame());
    expect(h.ws().frames("visibility")).toEqual([{ type: "visibility", visible: false }]);
  });

  const entry = (status: string, over: Partial<CallRecord> = {}): BroadcastMessage => ({
    id: "m-" + status,
    type: "call",
    channel: "dm:1:2",
    user_id: 2,
    author: bob,
    text: "Missed video call",
    reply_to: null,
    created_at: 2000,
    edited_at: null,
    attachments: [],
    call: { id: 1, media: "video", status, caller_id: 2, callee_id: 1, started_at: 1990, answered_at: null, ended_at: 2000, duration: 0, ...over },
  });

  it("counts a call entry as unread only when it was missed", () => {
    const h = harness();
    h.server({ type: "message", channel: "dm:1:2", message: { ...entry("completed", { answered_at: 1995, duration: 5 }), text: "Video call · 0:05" } });
    h.server({ type: "message", channel: "dm:1:2", message: { ...entry("declined"), text: "Declined video call" } });
    expect(h.store.dmStateFor("dm:1:2").unreadCount).toBe(0);
    expect(h.notify).not.toHaveBeenCalled();
    h.server({ type: "message", channel: "dm:1:2", message: entry("no_answer") });
    expect(h.store.dmStateFor("dm:1:2").unreadCount).toBe(1);
    expect(h.notify).toHaveBeenCalledWith(expect.objectContaining({ title: "Bob", body: "Missed video call" }));
  });

  it("keeps delivery receipts off call entries", () => {
    const h = harness();
    h.server({
      type: "message",
      channel: "dm:1:2",
      message: { ...entry("completed"), user_id: 1, call: { ...entry("completed").call!, caller_id: 1, callee_id: 2 } },
    });
    expect(h.store.deliveryStatus("dm:1:2")).toBeNull();
  });
});

describe("call formatting", () => {
  const rec = (status: string, over: Partial<CallRecord> = {}): CallRecord => ({
    id: 1, media: "video", status, caller_id: 2, callee_id: 1, started_at: 0, answered_at: null, ended_at: 0, duration: 0, ...over,
  });

  it("labels entries from each side", () => {
    expect(callEntryText(rec("completed", { duration: 272 }), 1)).toEqual({ title: "Video call", detail: "4:32", missed: false, outgoing: false });
    expect(callEntryText(rec("no_answer"), 1)).toEqual({ title: "Missed video call", detail: "", missed: true, outgoing: false });
    expect(callEntryText(rec("no_answer"), 2)).toEqual({ title: "Video call", detail: "No answer", missed: false, outgoing: true });
    expect(callEntryText(rec("declined", { media: "audio" }), 2).detail).toBe("Declined");
    expect(callEntryText(rec("failed"), 1)).toMatchObject({ detail: "Couldn't connect", missed: false });
  });

  it("previews call entries in the sidebar", () => {
    const m = (status: string): HistoryMessage => ({
      id: "x", type: "call", channel: "dm:1:2", user_id: 2, text: "", reply_to: null, created_at: 1, edited_at: null, attachments: [], call: rec(status, { duration: 61 }),
    });
    expect(lastMessagePreviewFor([m("completed")], 1)).toBe("Video call · 1:01");
    expect(lastMessagePreviewFor([m("busy")], 1)).toBe("Missed video call");
    expect(lastMessagePreviewFor([m("busy")], 2)).toBe("Video call · Busy");
  });

  it("formats durations like the server", () => {
    expect(fmtCallDuration(0)).toBe("0:00");
    expect(fmtCallDuration(272)).toBe("4:32");
    expect(fmtCallDuration(3725)).toBe("1:02:05");
    expect(callOutcomeText("busy_self", "outgoing")).toBe("You're on a call on another device");
  });
});
