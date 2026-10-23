/**
 * iOS: calls go through the system call UI (modules/system-calls): CallKit,
 * or LiveCommunicationKit in mainland China, where the App Store doesn't
 * allow CallKit. The system rings with its own incoming-call screen (on the
 * lock screen too), shows the call in the status bar and Control Center, and
 * owns the audio session while a call is reported: it activates the
 * session, then WebRTC's audio starts (modules/system-calls/ios/CallAudio.swift).
 *
 * This file mirrors the shared CallManager (store.calls) into the system and
 * the system's buttons back into it. A call the system refuses (a Focus or
 * Do Not Disturb filtered it, or any error) falls back to the app's own
 * ringing and audio session (callAudio), as before CallKit; a filtered call
 * doesn't ring at all. Without VoIP push (it needs a paid Apple developer
 * account) the system hears of a call only while the app runs.
 */
import { useEffect, useRef, useSyncExternalStore } from "react";
import type { CallOutcome, CallSession, ChatStore, NotifyEvent } from "@alexmessages/shared";
import { SystemCalls, type SystemCallEnd } from "../../modules/system-calls";
import { handOffMedia } from "../media";
import { presentCallNotification, presentMessageNotification } from "../notify";
import { callAudio } from "./callAudio";
import type { CallRoute, CallSounds } from "./route";

/** "pending" until the system took the call ("system") or refused it ("own"). */
type Mode = "pending" | "system" | "own";

interface Tracked {
  key: number;
  direction: CallSession["direction"];
  mode: Mode;
  /** A Focus or Do Not Disturb filtered the call: it doesn't ring. */
  filtered: boolean;
  phase: CallSession["phase"];
  micOn: boolean;
  /** Answered in the system UI (so the app doesn't ask the system to answer). */
  systemAnswered: boolean;
  /** Ended in the system UI. */
  systemEnded: boolean;
  connected: boolean;
  ended: boolean;
  /** The ringing banner, held until the mode is known. */
  notice: NotifyEvent | null;
  /** The system hears of each change in order, after the report. */
  chain: Promise<void>;
}

const enabled = SystemCalls.kind !== "none";

let current: Tracked | null = null;
let version = 0;
const listeners = new Set<() => void>();

function changed(): void {
  version++;
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

const noop = () => {};

/** Nothing plays until the system has taken or refused the call. */
const PENDING: CallRoute = {
  ring: false,
  sounds: {
    startRingtone: noop,
    stopRingtone: noop,
    start: noop,
    stop: noop,
    startRingback: noop,
    stopRingback: noop,
    setSpeaker: noop,
    keepScreenOn: noop,
  },
};

const systemSounds: CallSounds = {
  // The system rings.
  startRingtone: noop,
  stopRingtone: noop,
  // The system configures and activates the audio session.
  start: () => void SystemCalls.setInCall(true),
  stop: () => void SystemCalls.setInCall(false),
  startRingback: () => void SystemCalls.startRingback(),
  stopRingback: () => void SystemCalls.stopRingback(),
  setSpeaker: (on) => void SystemCalls.setSpeaker(on),
  keepScreenOn: noop,
};

const SYSTEM: CallRoute = { ring: false, sounds: systemSounds };
const OWN: CallRoute = { ring: true, sounds: callAudio };
const OWN_SILENT: CallRoute = { ring: false, sounds: callAudio };

export function attachSystemCalls(store: ChatStore): () => void {
  if (!enabled) return () => {};
  const subs = [
    SystemCalls.addListener("onAnswer", ({ key }) => {
      const t = current;
      if (t?.key !== key) return;
      t.systemAnswered = true;
      void store.calls.accept();
    }),
    SystemCalls.addListener("onEnd", ({ key }) => {
      const t = current;
      if (t?.key !== key) return;
      t.systemEnded = true;
      store.calls.hangUp();
    }),
    SystemCalls.addListener("onMute", ({ key, muted }) => {
      const t = current;
      const s = store.state.call;
      if (t?.key !== key || s?.key !== key || s.micOn !== muted) return;
      // The system already shows it: don't send it back.
      t.micOn = !muted;
      store.calls.toggleMic();
    }),
    SystemCalls.addListener("onReset", () => {
      if (current) current.systemEnded = true;
      store.calls.hangUp();
    }),
  ];
  const off = store.subscribe(() => sync(store.state.call));
  sync(store.state.call);
  return () => {
    off();
    for (const sub of subs) sub.remove();
  };
}

function sync(call: CallSession | null): void {
  const t = current;
  if (t && call?.key !== t.key) {
    // The session went away without an end seen here (logout, a new store).
    end(t, "hung_up");
    current = null;
    changed();
  }
  if (!call) return;
  if (!current) {
    if (call.phase === "ended") return;
    current = begin(call);
    changed();
    return;
  }
  step(current, call);
}

function begin(call: CallSession): Tracked {
  const t: Tracked = {
    key: call.key,
    direction: call.direction,
    mode: "pending",
    filtered: false,
    phase: call.phase,
    micOn: call.micOn,
    systemAnswered: false,
    systemEnded: false,
    connected: false,
    ended: false,
    notice: null,
    chain: Promise.resolve(),
  };
  const handle = String(call.peer.id);
  const name = call.peer.display_name || call.peer.username;
  const video = call.media === "video";
  // In-app media stops first. A player stopped now deactivates the audio
  // session 100 ms later, which would cut the session the system activates
  // for an outgoing call; an incoming one activates it only when answered.
  const wait = handOffMedia(call.key) - Date.now();
  const report =
    call.direction === "incoming"
      ? SystemCalls.reportIncoming(call.key, handle, name, video)
      : delay(wait).then(() => SystemCalls.startOutgoing(call.key, handle, name, video));
  t.chain = report.then(
    () => setMode(t, "system"),
    (e: unknown) => setMode(t, "own", (e as { code?: string } | null)?.code === "ERR_FILTERED")
  );
  return t;
}

function step(t: Tracked, call: CallSession): void {
  if (t.ended) return;
  const prev = t.phase;
  t.phase = call.phase;
  if (call.phase === "ended") {
    end(t, call.outcome ?? "hung_up");
    return;
  }
  if (call.micOn !== t.micOn) {
    t.micOn = call.micOn;
    toSystem(t, () => SystemCalls.requestMute(t.key, !call.micOn));
  }
  if (t.direction === "outgoing") {
    if (prev === "outgoing" && call.phase !== "outgoing") {
      toSystem(t, () => SystemCalls.reportConnecting(t.key));
    }
    if (call.phase === "active" && !t.connected) {
      t.connected = true;
      toSystem(t, () => SystemCalls.reportConnected(t.key));
    }
  } else if (prev === "incoming" && call.phase !== "incoming" && !t.systemAnswered) {
    // Answered in the app: the system answers too, then activates the audio
    // session. If it can't, the app runs the call's audio itself.
    toSystem(t, () => SystemCalls.requestAnswer(t.key).catch(() => setMode(t, "own")));
  }
}

function end(t: Tracked, outcome: CallOutcome): void {
  if (t.ended) return;
  t.ended = true;
  t.notice = null;
  if (t.systemEnded) return;
  if (outcome === "hung_up") {
    toSystem(t, () => SystemCalls.requestEnd(t.key));
  } else {
    toSystem(t, () => SystemCalls.reportEnded(t.key, endReason(t.direction, outcome)));
  }
}

/** Runs a system update in order, once the report settled, if the system has the call. */
function toSystem(t: Tracked, update: () => Promise<unknown>): void {
  t.chain = t.chain
    .then(() => (t.mode === "system" ? update() : undefined))
    .then(noop, noop);
}

function setMode(t: Tracked, mode: Mode, filtered = false): void {
  t.mode = mode;
  t.filtered = filtered;
  if (t === current) changed();
  const notice = t.notice;
  t.notice = null;
  if (notice && mode === "own" && !filtered && !t.ended && t.phase === "incoming") {
    void presentCallNotification(notice);
  }
}

function endReason(direction: CallSession["direction"], outcome: CallOutcome): SystemCallEnd {
  switch (outcome) {
    case "completed":
    case "cancelled":
    case "busy":
      return "remoteEnded";
    case "declined":
      // An incoming call declined by the server: another of my devices declined it.
      return direction === "incoming" ? "declinedElsewhere" : "remoteEnded";
    case "no_answer":
      return "unanswered";
    case "answered_elsewhere":
      return "answeredElsewhere";
    default:
      return "failed";
  }
}

function delay(ms: number): Promise<void> {
  return ms > 0 ? new Promise((resolve) => setTimeout(resolve, ms)) : Promise.resolve();
}

export function useCallRoute(call: CallSession | null, _ready: boolean): CallRoute {
  useSyncExternalStore(subscribe, () => version);
  if (!enabled || !call) return OWN;
  const t = current?.key === call.key ? current : null;
  if (!t) return OWN;
  if (t.mode === "pending") return PENDING;
  if (t.mode === "system") return SYSTEM;
  return t.filtered ? OWN_SILENT : OWN;
}

/** The audio route moved on or off the loudspeaker outside the app (Control Center, a headset). */
export function useSystemSpeaker(onChange: (on: boolean) => void): void {
  const ref = useRef(onChange);
  ref.current = onChange;
  useEffect(() => {
    if (!enabled) return;
    const sub = SystemCalls.addListener("onSpeaker", ({ on }) => ref.current(on));
    return () => sub.remove();
  }, []);
}

/** The system shows its own incoming-call screen; the banner only goes up
 *  for a call the system refused (and that wasn't filtered). */
export function notifyIncomingCall(n: NotifyEvent): void {
  if (!enabled) {
    void presentCallNotification(n);
    return;
  }
  const t = current;
  if (!t || t.ended || t.phase !== "incoming") return;
  if (t.mode === "pending") t.notice = n;
  else if (t.mode === "own" && !t.filtered) void presentCallNotification(n);
}

export function notifyMissedCall(n: NotifyEvent): void {
  void presentMessageNotification(n);
}

/** The system shows calls on the lock screen; the app never shows over it. */
export function lockedOverCall(): boolean {
  return false;
}

export async function unlockToLeave(): Promise<boolean> {
  return true;
}
