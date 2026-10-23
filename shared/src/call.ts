/**
 * CallManager — one device's side of a 1:1 DM call.
 *
 * The server (internal/webapp/calls.go) owns each call's lifecycle: who is
 * ringing, which two devices are on the call, and how it ended. It relays
 * opaque CallSignal payloads between those two devices over /ws. This module
 * runs the rest on the device: it captures the microphone and camera, drives
 * the WebRTC peer connection, and publishes the call's state for the UI.
 *
 * Negotiation has fixed roles. The caller creates every offer (the first on
 * call_accepted, later ones only for ICE restarts) and the callee only
 * answers, so offers never collide and no rollback is needed. The caller's
 * first offer carries an audio and a video transceiver, both sendrecv, so
 * muting, turning the camera on or off, and switching cameras are all
 * replaceTrack — never a renegotiation. Mic and camera switches reach the
 * peer as "state" signals rather than inferred from track mute events.
 *
 * WebRTC is reached through CallPlatform, so the same code drives the
 * browser's RTCPeerConnection and react-native-webrtc.
 */

import { dmChannelFor } from "./dm";
import type {
  CallEndReason,
  CallInfo,
  CallMedia,
  CallSignal,
  ClientMessage,
  IceServer,
  PublicUser,
  ServerEvent,
} from "./types";

// ---------- the WebRTC surface the manager uses ----------
// Structural, so the browser's objects and react-native-webrtc's both fit
// (each platform adapter casts its own types to these).

export interface CallTrack {
  readonly kind: string;
  enabled: boolean;
  stop(): void;
}

export interface CallStream {
  getTracks(): CallTrack[];
  getAudioTracks(): CallTrack[];
  getVideoTracks(): CallTrack[];
}

export interface CallSender {
  readonly track: CallTrack | null;
  replaceTrack(track: CallTrack | null): Promise<void>;
  /** Missing on react-native-webrtc and older browsers. */
  setStreams?(...streams: CallStream[]): void;
}

export interface CallTransceiver {
  readonly sender: CallSender;
  readonly receiver: { readonly track: CallTrack };
  direction: string;
}

export interface CallDescription {
  type: string;
  sdp?: string;
}

export interface CallIceCandidate {
  candidate: string;
  sdpMid?: string | null;
  sdpMLineIndex?: number | null;
}

/** What the manager reads from the icecandidate and track events. */
export interface CallPeerEvent {
  candidate?: CallIceCandidate | null;
  track?: CallTrack;
}

export interface CallPeerConnection {
  readonly remoteDescription: CallDescription | null;
  readonly iceConnectionState: string;
  addTransceiver(
    trackOrKind: CallTrack | "audio" | "video",
    init: { direction: "sendrecv"; streams: CallStream[] },
  ): CallTransceiver;
  getTransceivers(): CallTransceiver[];
  createOffer(options?: { iceRestart?: boolean }): Promise<CallDescription>;
  createAnswer(): Promise<CallDescription>;
  setLocalDescription(description: CallDescription): Promise<void>;
  setRemoteDescription(description: CallDescription): Promise<void>;
  addIceCandidate(candidate: CallIceCandidate): Promise<void>;
  addEventListener(
    type: "icecandidate" | "track" | "iceconnectionstatechange",
    listener: (event: CallPeerEvent) => void,
  ): void;
  close(): void;
}

export type CallFacing = "user" | "environment";

export interface CallMediaConstraints {
  audio: boolean;
  video: false | { facingMode: CallFacing };
}

/** The platform's WebRTC entry points. */
export interface CallPlatform {
  createPeerConnection(config: { iceServers: IceServer[] }): CallPeerConnection;
  /** getUserMedia: rejects with a DOMException-style `name` on failure. */
  getUserMedia(constraints: CallMediaConstraints): Promise<CallStream>;
  /** A stream over existing tracks (the remote stream, the self view). */
  createStream(tracks: CallTrack[]): CallStream;
  /** Frees a createStream stream the session no longer shows; its tracks
   *  keep running. react-native-webrtc holds every stream natively until
   *  told; browsers collect them and leave this out. */
  releaseStream?(stream: CallStream): void;
}

// ---------- session state ----------

/**
 *  outgoing     placed by this device; ringing the peer
 *  incoming     ringing on this device
 *  connecting   answered; media not flowing yet
 *  active       media flowing
 *  reconnecting media (or this device's signaling) dropped; recovering
 *  ended        over — kept for CALL_ENDED_LINGER_MS so the UI can say why
 */
export type CallPhase = "outgoing" | "incoming" | "connecting" | "active" | "reconnecting" | "ended";

/** Why this device's call ended: the server's reason, or one from here —
 *  "hung_up" (this device ended it), "no_media" (no usable microphone),
 *  "failed" (answered, but media never connected), "lost" (media connected,
 *  then stayed lost), "busy_self" (another of your devices is on a call),
 *  "invalid" (that user can't be called). */
export type CallOutcome = CallEndReason | "hung_up" | "no_media" | "lost" | "busy_self" | "invalid";

export interface CallSession {
  /** Local identity, stable for the session's whole life (React keys). */
  key: number;
  /** Server call id; null until the server registers an outgoing call. */
  id: string | null;
  peer: PublicUser;
  channel: string;
  media: CallMedia;
  direction: "outgoing" | "incoming";
  phase: CallPhase;
  /** Outgoing: a device of the peer is ringing. */
  ringing: boolean;
  /** Local clock (ms) when the call was answered — the duration timer's 0. */
  answeredAt: number | null;
  micOn: boolean;
  cameraOn: boolean;
  facing: CallFacing;
  peerMicOn: boolean;
  peerCameraOn: boolean;
  /** This device's microphone and camera (the self view). */
  localStream: CallStream | null;
  /** The peer's audio and video. */
  remoteStream: CallStream | null;
  outcome: CallOutcome | null;
  /** Why media capture failed, in words for the user. */
  error: string | null;
}

/** How long an answered call may take to connect its media. */
export const CALL_CONNECT_TIMEOUT_MS = 30000;
/** How long connected media may stay lost before the call ends. */
export const CALL_RECONNECT_TIMEOUT_MS = 30000;
/** How long ICE may sit "disconnected" before an ICE restart. */
export const CALL_ICE_RESTART_DELAY_MS = 4000;
/** How long an ended session stays on screen. */
export const CALL_ENDED_LINGER_MS = 2000;
/** Cap on signals held while this device's socket reconnects. */
const OUTBOX_CAP = 256;

export type CallEvent = Extract<ServerEvent, { type: `call_${string}` }>;

export interface CallManagerDeps {
  /** Null when this client can't place or take calls. */
  platform: CallPlatform | null;
  send(message: ClientMessage): void;
  me(): PublicUser | null;
  /** Publishes the session (or its removal) — ChatStore re-renders. */
  changed(session: CallSession | null): void;
  toast(text: string, isErr?: boolean): void;
  debug(level: string, event: string, message: string, context?: unknown): void;
  /** Whether the app is on screen. */
  foreground(): boolean;
  /** A call rang while the app was in the background. */
  notifyIncoming?(session: CallSession): void;
}

type Timer = ReturnType<typeof setTimeout>;

const randomId = (): string => Math.random().toString(36).slice(2, 10) + Date.now().toString(36);

export class CallManager {
  session: CallSession | null = null;
  /** Names this device to the server for its whole life, so only this device
   *  can resume its side of a call after a reconnect. */
  readonly device = "d" + randomId();

  private deps: CallManagerDeps;
  private nextKey = 1;
  private role: "caller" | "callee" | null = null;
  private pc: CallPeerConnection | null = null;
  private iceServers: IceServer[] = [];
  private mic: CallTrack | null = null;
  private cam: CallTrack | null = null;
  private audioTx: CallTransceiver | null = null;
  private videoTx: CallTransceiver | null = null;
  private remoteTracks: CallTrack[] = [];
  /** Remote candidates that arrived before the remote description. */
  private remoteCandidates: CallIceCandidate[] = [];
  /** Local candidates gathered before their description went out; null once
   *  it has, when candidates are sent as they come. */
  private localCandidates: CallSignal[] | null = [];
  /** WebRTC operations run one at a time, in arrival order. */
  private chain: Promise<void> = Promise.resolve();
  /** False from a socket drop until the server confirms the resume. */
  private linkUp = true;
  private outbox: CallSignal[] = [];
  /** The client_call_id of this device's outgoing call. */
  private nonce: string | null = null;
  /** call_start sent for this nonce, but not yet named by the server. */
  private startSent: string | null = null;
  /** Nonces hung up before the server named their call. */
  private cancelled = new Set<string>();
  /** A call that rang while our own call_start was in flight. */
  private deferredIncoming: CallInfo | null = null;
  private everConnected = false;
  private cameraBusy = false;
  private timers: { connect?: Timer; restart?: Timer; giveUp?: Timer; linger?: Timer } = {};

  constructor(deps: CallManagerDeps) {
    this.deps = deps;
  }

  /** Whether this client can place and take calls. */
  get supported(): boolean {
    return this.deps.platform != null;
  }

  /** Whether a call is live on this device (not ended). */
  get busy(): boolean {
    return !!this.session && this.session.phase !== "ended";
  }

  // ---------- actions ----------

  /** Calls peer. Calling the person who is calling you answers them. */
  async start(peer: PublicUser, media: CallMedia): Promise<void> {
    const live = this.busy ? this.session : null;
    if (live) {
      if (live.phase === "incoming" && live.peer.id === peer.id) await this.accept();
      return;
    }
    const me = this.deps.me();
    if (!this.deps.platform) {
      this.deps.toast("Calls aren't available on this device", true);
      return;
    }
    if (!me || peer.id === me.id) return;
    const nonce = "k" + randomId();
    const session = this.open({
      id: null,
      peer,
      channel: dmChannelFor(me.id, peer.id),
      media,
      direction: "outgoing",
      phase: "outgoing",
    });
    this.deps.debug("info", "call_start", "Placing call", { peer: peer.id, media });
    if (!(await this.capture(session.key, media === "video"))) return;
    this.startSent = nonce;
    this.nonce = nonce;
    this.deps.send({ type: "call_start", peer_id: peer.id, media, client_call_id: nonce, device: this.device });
  }

  /** Answers the call ringing on this device. */
  async accept(): Promise<void> {
    const s = this.session;
    if (!s || s.phase !== "incoming" || !s.id) return;
    const id = s.id;
    this.update({ phase: "connecting" });
    if (!(await this.capture(s.key, s.media === "video"))) return;
    this.role = "callee";
    this.createPeer();
    this.deps.send({ type: "call_accept", call_id: id, device: this.device });
  }

  /** Ends the call: hangs up, cancels, or declines, depending on the phase. */
  hangUp(): void {
    if (this.busy) this.finish("hung_up", { tell: true });
  }

  toggleMic(): void {
    const s = this.session;
    if (!s || !this.busy || !this.mic) return;
    const on = !s.micOn;
    this.mic.enabled = on;
    this.update({ micOn: on });
    this.sendState();
  }

  /** Turns the camera off (releasing it) or on (capturing it again). */
  async toggleCamera(): Promise<void> {
    const s = this.session;
    const platform = this.deps.platform;
    if (!s || !this.busy || !platform || this.cameraBusy) return;
    if (this.cam) {
      const cam = this.cam;
      this.cam = null;
      await this.videoTx?.sender.replaceTrack(null).catch(() => undefined);
      cam.stop();
      this.update({ cameraOn: false, localStream: this.selfView() });
      this.sendState();
      return;
    }
    this.cameraBusy = true;
    try {
      const track = await this.openCamera(s.facing);
      if (this.session?.key !== s.key || !this.busy) {
        track.stop();
        return;
      }
      this.cam = track;
      await this.videoTx?.sender.replaceTrack(track).catch(() => undefined);
      this.update({ cameraOn: true, localStream: this.selfView() });
      this.sendState();
    } catch {
      this.deps.toast("Camera unavailable", true);
    } finally {
      this.cameraBusy = false;
    }
  }

  /** Flips between the front and back cameras. */
  async switchCamera(): Promise<void> {
    const s = this.session;
    if (!s || !this.busy || !this.cam || this.cameraBusy) return;
    const facing: CallFacing = s.facing === "user" ? "environment" : "user";
    this.cameraBusy = true;
    const old = this.cam;
    try {
      // Phones run one camera at a time: release the old one first.
      old.stop();
      const track = await this.openCamera(facing);
      if (this.session?.key !== s.key || !this.busy || this.cam !== old) {
        track.stop();
        return;
      }
      this.cam = track;
      await this.videoTx?.sender.replaceTrack(track).catch(() => undefined);
      this.update({ facing, localStream: this.selfView() });
    } catch {
      this.cam = null;
      await this.videoTx?.sender.replaceTrack(null).catch(() => undefined);
      this.update({ cameraOn: false, localStream: this.selfView() });
      this.sendState();
      this.deps.toast("Couldn't switch cameras", true);
    } finally {
      this.cameraBusy = false;
    }
  }

  /** Clears an ended session off screen now. */
  dismiss(): void {
    if (this.session && !this.busy) this.clear();
  }

  // ---------- socket lifecycle (from ChatStore) ----------

  /** The control socket dropped: signals wait until the call is resumed. */
  linkDown(): void {
    if (this.busy && this.session?.id) this.linkUp = false;
  }

  /** The control socket is back and init applied: rebind this device. */
  linkReady(): void {
    const s = this.session;
    if (!this.busy || !s?.id) {
      this.linkUp = true;
      return;
    }
    this.linkUp = false;
    this.deps.send({ type: "call_resume", call_id: s.id, device: this.device });
  }

  /** Logout: ends any call and forgets everything. */
  destroy(): void {
    this.hangUp();
    this.clear();
  }

  // ---------- server events ----------

  handle(ev: CallEvent): void {
    switch (ev.type) {
      case "call_outgoing":
        this.onOutgoing(ev.client_call_id, ev.call);
        break;
      case "call_incoming":
        this.onIncoming(ev.call);
        break;
      case "call_ringing":
        if (this.isLive(ev.call_id)) this.update({ ringing: true });
        break;
      case "call_accepted":
        this.onAccepted(ev.client_call_id, ev.call);
        break;
      case "call_signal":
        if (this.isLive(ev.call_id)) {
          const payload = ev.payload;
          this.enqueue(() => this.onSignal(payload));
        }
        break;
      case "call_resumed":
        this.onResumed(ev.call);
        break;
      case "call_ended":
        this.onEnded(ev.call_id, ev.client_call_id, ev.reason);
        break;
      case "call_error":
        this.onError(ev.client_call_id, ev.error);
        break;
    }
  }

  private onOutgoing(nonce: string, call: CallInfo): void {
    if (this.startSent === nonce) this.startSent = null;
    if (this.cancelled.delete(nonce)) {
      // Hung up before the server named the call: end it there too.
      this.deps.send({ type: "call_hangup", call_id: call.id });
      return;
    }
    const s = this.session;
    if (!s || !this.busy || this.nonce !== nonce) return;
    this.iceServers = call.ice_servers || [];
    this.update({ id: call.id, peer: call.peer, channel: call.channel });
  }

  private onIncoming(call: CallInfo): void {
    const s = this.session;
    if (s && this.busy) {
      // Redelivered after a reconnect: already ringing here.
      if (s.id === call.id) return;
      // Our own call_start is in flight; the server answers it next — by
      // joining us to this call if it comes from the same peer, or with
      // busy_self, after which this call shows.
      if (s.direction === "outgoing" && !s.id) this.deferredIncoming = call;
      return;
    }
    if (!this.deps.platform) return;
    this.iceServers = call.ice_servers || [];
    const session = this.open({
      id: call.id,
      peer: call.peer,
      channel: call.channel,
      media: call.media,
      direction: "incoming",
      phase: "incoming",
    });
    this.deps.debug("info", "call_incoming", "Incoming call", { call: call.id, media: call.media });
    if (!this.deps.foreground()) this.deps.notifyIncoming?.(session);
  }

  private onAccepted(nonce: string, call: CallInfo): void {
    if (nonce && this.cancelled.delete(nonce)) {
      // Hung up before the server answered our crossed call_start by joining
      // us to the peer's call: end it there too, and here, where that call
      // may have rung or been held back meanwhile.
      if (this.deferredIncoming?.id === call.id) this.deferredIncoming = null;
      if (this.isLive(call.id)) this.finish("hung_up", { tell: true });
      else this.deps.send({ type: "call_hangup", call_id: call.id });
      return;
    }
    const s = this.session;
    if (!s || !this.busy) return;
    if (!((nonce && this.nonce === nonce) || s.id === call.id)) return;
    this.deferredIncoming = null;
    this.startSent = null;
    this.iceServers = call.ice_servers || this.iceServers;
    const me = this.deps.me();
    this.update({
      id: call.id,
      peer: call.peer,
      channel: call.channel,
      media: call.media,
      ringing: false,
      phase: s.phase === "active" || s.phase === "reconnecting" ? s.phase : "connecting",
      answeredAt: s.answeredAt ?? Date.now(),
    });
    if (me && call.caller_id === me.id) {
      if (this.role !== "caller") {
        this.role = "caller";
        this.createPeer();
        this.enqueue(() => this.offer(false));
      }
    } else {
      // We answered — or our call crossed the peer's and the server joined
      // us to theirs as its callee.
      this.role = "callee";
      this.createPeer();
    }
    this.sendState();
    this.armConnectTimeout();
  }

  private onResumed(call: CallInfo): void {
    const s = this.session;
    if (!s || !this.busy || s.id !== call.id) return;
    this.linkUp = true;
    const held = this.outbox.splice(0);
    for (const payload of held) this.deps.send({ type: "call_signal", call_id: call.id, payload });
    if (call.state === "active" && !this.role) {
      // Answered while this device was reconnecting.
      this.onAccepted("", call);
      return;
    }
    const ice = this.pc?.iceConnectionState;
    if (ice === "failed" || ice === "disconnected") this.requestRestart();
  }

  private onEnded(callId: string, nonce: string | undefined, reason: CallEndReason): void {
    if (nonce) this.cancelled.delete(nonce);
    if (this.deferredIncoming?.id === callId) {
      this.deferredIncoming = null;
      return;
    }
    const s = this.session;
    if (!s || !this.busy) return;
    if (s.id !== callId && !(nonce && this.nonce === nonce)) return;
    this.finish(reason);
  }

  private onError(nonce: string, error: "invalid" | "busy_self"): void {
    if (this.startSent === nonce) this.startSent = null;
    if (!this.busy || this.nonce !== nonce) return;
    this.finish(error);
    // Our call never started; a call that rang for us meanwhile shows now.
    const deferred = this.deferredIncoming;
    this.deferredIncoming = null;
    if (deferred) this.onIncoming(deferred);
  }

  // ---------- signaling ----------

  private async onSignal(payload: CallSignal): Promise<void> {
    const pc = this.pc;
    switch (payload.kind) {
      case "offer":
        if (pc && this.role === "callee") await this.answer(pc, payload.sdp);
        break;
      case "answer":
        if (pc && this.role === "caller") {
          await pc.setRemoteDescription({ type: "answer", sdp: payload.sdp });
          await this.flushRemoteCandidates(pc);
        }
        break;
      case "candidate": {
        if (!pc) break;
        const c: CallIceCandidate = {
          candidate: payload.candidate,
          sdpMid: payload.sdpMid,
          sdpMLineIndex: payload.sdpMLineIndex,
        };
        if (!pc.remoteDescription) this.remoteCandidates.push(c);
        else await pc.addIceCandidate(c).catch((e) => this.rtcError("add_candidate", e));
        break;
      }
      case "state":
        this.update({ peerMicOn: !!payload.mic, peerCameraOn: !!payload.camera });
        break;
      case "restart":
        if (this.role === "caller") this.requestRestart();
        break;
    }
  }

  private async offer(iceRestart: boolean): Promise<void> {
    const pc = this.pc;
    if (!pc || this.role !== "caller") return;
    this.localCandidates = [];
    const offer = await pc.createOffer(iceRestart ? { iceRestart: true } : undefined);
    await pc.setLocalDescription(offer);
    if (this.pc !== pc) return;
    this.signal({ kind: "offer", sdp: offer.sdp ?? "" });
    this.flushLocalCandidates();
  }

  private async answer(pc: CallPeerConnection, sdp: string): Promise<void> {
    await pc.setRemoteDescription({ type: "offer", sdp });
    if (!this.audioTx && !this.videoTx) {
      // The first offer: send our tracks on the transceivers it created.
      for (const t of pc.getTransceivers()) {
        const kind = t.receiver.track?.kind;
        if (kind === "audio" && !this.audioTx) this.audioTx = t;
        else if (kind === "video" && !this.videoTx) this.videoTx = t;
      }
      const streams = this.senderStreams();
      for (const [tx, track] of [
        [this.audioTx, this.mic],
        [this.videoTx, this.cam],
      ] as const) {
        if (!tx) continue;
        tx.direction = "sendrecv";
        await tx.sender.replaceTrack(track);
        tx.sender.setStreams?.(...streams);
      }
    }
    await this.flushRemoteCandidates(pc);
    this.localCandidates = [];
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    if (this.pc !== pc) return;
    this.signal({ kind: "answer", sdp: answer.sdp ?? "" });
    this.flushLocalCandidates();
  }

  private async flushRemoteCandidates(pc: CallPeerConnection): Promise<void> {
    const held = this.remoteCandidates.splice(0);
    for (const c of held) await pc.addIceCandidate(c).catch((e) => this.rtcError("add_candidate", e));
  }

  private flushLocalCandidates(): void {
    const held = this.localCandidates ?? [];
    this.localCandidates = null;
    for (const c of held) this.signal(c);
  }

  /** Sends a payload to the peer, holding it while this device reconnects. */
  private signal(payload: CallSignal): void {
    const id = this.session?.id;
    if (!id || !this.busy) return;
    if (!this.linkUp) {
      if (this.outbox.length < OUTBOX_CAP) this.outbox.push(payload);
      return;
    }
    this.deps.send({ type: "call_signal", call_id: id, payload });
  }

  private sendState(): void {
    const s = this.session;
    if (!s || !this.role) return;
    this.signal({ kind: "state", mic: s.micOn, camera: s.cameraOn });
  }

  /** ICE restarts come from the caller; the callee asks for one. */
  private requestRestart(): void {
    clearTimeout(this.timers.restart);
    this.timers.restart = undefined;
    if (this.role === "caller") this.enqueue(() => this.offer(true));
    else if (this.role === "callee") this.signal({ kind: "restart" });
  }

  /** Queues a WebRTC step; it is skipped if its call is over by then. */
  private enqueue(op: () => Promise<void>): void {
    const key = this.session?.key;
    this.chain = this.chain
      .then(() => (this.session?.key === key && this.busy ? op() : undefined))
      .catch((e) => this.rtcError("negotiation", e));
  }

  private rtcError(step: string, e: unknown): void {
    this.deps.debug("warn", "call_rtc_error", `${step}: ${e instanceof Error ? e.message : String(e)}`);
  }

  // ---------- media ----------

  /** Captures the mic (and camera for video calls). A video call without a
   *  usable camera goes ahead with the camera off; without a microphone the
   *  call ends with outcome "no_media". */
  private async capture(key: number, wantVideo: boolean): Promise<boolean> {
    const platform = this.deps.platform;
    if (!platform) return false;
    let stream: CallStream | null = null;
    let error: unknown = null;
    try {
      stream = await platform.getUserMedia({ audio: true, video: wantVideo ? { facingMode: "user" } : false });
    } catch (e) {
      error = e;
      if (wantVideo && !isPermissionError(e)) {
        try {
          stream = await platform.getUserMedia({ audio: true, video: false });
        } catch (e2) {
          error = e2;
        }
      }
    }
    if (this.session?.key !== key || !this.busy) {
      for (const t of stream?.getTracks() ?? []) t.stop();
      return false;
    }
    const mic = stream?.getAudioTracks()[0] ?? null;
    if (!stream || !mic) {
      for (const t of stream?.getTracks() ?? []) t.stop();
      this.deps.debug("warn", "call_media_error", "Microphone capture failed", { error: errorName(error) });
      this.finish("no_media", { tell: true, error: captureErrorText(error, wantVideo) });
      return false;
    }
    this.mic = mic;
    this.cam = stream.getVideoTracks()[0] ?? null;
    this.update({ micOn: true, cameraOn: !!this.cam, localStream: this.selfView() });
    return true;
  }

  private async openCamera(facing: CallFacing): Promise<CallTrack> {
    const stream = await this.deps.platform!.getUserMedia({ audio: false, video: { facingMode: facing } });
    const track = stream.getVideoTracks()[0];
    if (!track) throw new Error("no camera track");
    return track;
  }

  private selfView(): CallStream | null {
    const tracks = [this.mic, this.cam].filter((t): t is CallTrack => !!t);
    return tracks.length && this.deps.platform ? this.deps.platform.createStream(tracks) : null;
  }

  /** The stream senders are associated with, so the peer sees one stream. */
  private senderStreams(): CallStream[] {
    const view = this.session?.localStream;
    return view ? [view] : [];
  }

  private createPeer(): void {
    if (this.pc || !this.deps.platform) return;
    const platform = this.deps.platform;
    const pc = platform.createPeerConnection({ iceServers: this.iceServers });
    this.pc = pc;
    this.localCandidates = [];
    this.remoteCandidates = [];
    this.remoteTracks = [];
    pc.addEventListener("icecandidate", (ev) => {
      const c = ev.candidate;
      if (this.pc !== pc || !c || !c.candidate) return;
      const payload: CallSignal = {
        kind: "candidate",
        candidate: c.candidate,
        sdpMid: c.sdpMid ?? null,
        sdpMLineIndex: c.sdpMLineIndex ?? null,
      };
      if (this.localCandidates) this.localCandidates.push(payload);
      else this.signal(payload);
    });
    pc.addEventListener("track", (ev) => {
      const track = ev.track;
      if (this.pc !== pc || !track) return;
      this.remoteTracks = [...this.remoteTracks.filter((t) => t.kind !== track.kind), track];
      this.update({ remoteStream: platform.createStream(this.remoteTracks) });
    });
    pc.addEventListener("iceconnectionstatechange", () => {
      if (this.pc === pc) this.onIceState(pc.iceConnectionState);
    });
    if (this.role === "caller") {
      const streams = this.senderStreams();
      this.audioTx = pc.addTransceiver(this.mic ?? "audio", { direction: "sendrecv", streams });
      this.videoTx = pc.addTransceiver(this.cam ?? "video", { direction: "sendrecv", streams });
    }
  }

  private onIceState(state: string): void {
    const s = this.session;
    if (!s || !this.busy) return;
    this.deps.debug("debug", "call_ice", state, { call: s.id });
    switch (state) {
      case "connected":
      case "completed":
        this.everConnected = true;
        for (const k of ["connect", "restart", "giveUp"] as const) {
          clearTimeout(this.timers[k]);
          this.timers[k] = undefined;
        }
        if (s.phase !== "active") this.update({ phase: "active" });
        break;
      case "disconnected":
        if (this.everConnected) this.update({ phase: "reconnecting" });
        this.timers.restart ??= setTimeout(() => this.requestRestart(), CALL_ICE_RESTART_DELAY_MS);
        this.armGiveUp();
        break;
      case "failed":
        if (this.everConnected) this.update({ phase: "reconnecting" });
        this.requestRestart();
        this.armGiveUp();
        break;
    }
  }

  private armConnectTimeout(): void {
    if (this.everConnected || this.timers.connect) return;
    this.timers.connect = setTimeout(() => {
      this.timers.connect = undefined;
      if (this.busy && !this.everConnected) this.finish("failed", { tell: true });
    }, CALL_CONNECT_TIMEOUT_MS);
  }

  private armGiveUp(): void {
    if (!this.everConnected || this.timers.giveUp) return;
    this.timers.giveUp = setTimeout(() => {
      this.timers.giveUp = undefined;
      if (this.busy) this.finish("lost", { tell: true });
    }, CALL_RECONNECT_TIMEOUT_MS);
  }

  // ---------- session bookkeeping ----------

  private open(init: Pick<CallSession, "id" | "peer" | "channel" | "media" | "direction" | "phase">): CallSession {
    this.clear();
    this.session = {
      ...init,
      key: this.nextKey++,
      ringing: false,
      answeredAt: null,
      micOn: true,
      cameraOn: init.media === "video",
      facing: "user",
      peerMicOn: true,
      peerCameraOn: init.media === "video",
      localStream: null,
      remoteStream: null,
      outcome: null,
      error: null,
    };
    this.deps.changed(this.session);
    return this.session;
  }

  private update(patch: Partial<CallSession>): void {
    const prev = this.session;
    if (!prev) return;
    this.session = { ...prev, ...patch };
    this.deps.changed(this.session);
    this.releaseStreams(prev, this.session);
  }

  /** Frees the streams prev showed that next no longer does. */
  private releaseStreams(prev: CallSession, next: CallSession | null): void {
    for (const k of ["localStream", "remoteStream"] as const) {
      const old = prev[k];
      if (old && old !== next?.[k]) this.deps.platform?.releaseStream?.(old);
    }
  }

  /** Whether callId is this device's live call. */
  private isLive(callId: string): boolean {
    return this.busy && this.session?.id === callId;
  }

  /** Ends this device's call. tell: the end started here, so the server is
   *  told — a hang-up, marked "failed" for an answered call whose media never
   *  connected. */
  private finish(outcome: CallOutcome, opts: { tell?: boolean; error?: string } = {}): void {
    const s = this.session;
    if (!s || !this.busy) return;
    if (opts.tell) {
      if (s.id) {
        const failed = outcome === "failed" && this.role != null;
        this.deps.send({ type: "call_hangup", call_id: s.id, ...(failed ? { reason: "failed" as const } : {}) });
      } else if (this.startSent) {
        this.cancelled.add(this.startSent);
      }
    }
    this.deps.debug("info", "call_end", "Call ended", { call: s.id, outcome });
    this.release();
    this.update({ phase: "ended", outcome, error: opts.error ?? null, ringing: false, localStream: null, remoteStream: null });
    const key = s.key;
    this.timers.linger = setTimeout(() => {
      if (this.session?.key === key) this.clear();
    }, CALL_ENDED_LINGER_MS);
  }

  /** Stops the media and the peer connection. */
  private release(): void {
    for (const k of ["connect", "restart", "giveUp"] as const) {
      clearTimeout(this.timers[k]);
      this.timers[k] = undefined;
    }
    this.mic?.stop();
    this.cam?.stop();
    this.mic = this.cam = null;
    try {
      this.pc?.close();
    } catch {
      /* already closed */
    }
    this.pc = null;
    this.audioTx = this.videoTx = null;
    this.remoteTracks = [];
    this.remoteCandidates = [];
    this.localCandidates = [];
    this.outbox = [];
    this.role = null;
    this.linkUp = true;
    this.everConnected = false;
    this.startSent = null;
    this.nonce = null;
  }

  private clear(): void {
    clearTimeout(this.timers.linger);
    this.timers.linger = undefined;
    if (this.busy) this.release();
    const prev = this.session;
    if (prev) {
      this.session = null;
      this.deps.changed(null);
      this.releaseStreams(prev, null);
    }
  }
}

function errorName(e: unknown): string {
  return (e as { name?: string } | null)?.name || "";
}

function isPermissionError(e: unknown): boolean {
  const name = errorName(e);
  return name === "NotAllowedError" || name === "SecurityError" || name === "PermissionDeniedError";
}

/** The words shown when the microphone (or camera) can't be captured. */
export function captureErrorText(e: unknown, video: boolean): string {
  switch (errorName(e)) {
    case "NotAllowedError":
    case "PermissionDeniedError":
    case "SecurityError":
      return video ? "Allow camera and microphone access to call." : "Allow microphone access to call.";
    case "NotFoundError":
      return "No microphone found.";
    case "NotReadableError":
      return "Your microphone is in use by another app.";
    default:
      return "Couldn't start your microphone.";
  }
}
