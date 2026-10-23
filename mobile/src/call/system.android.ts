/**
 * Android: the ring service (modules/call-ringer) keeps its own connection
 * to the server while the app is closed, and rings calls with a call
 * notification (Answer and Decline, and a full-screen call screen over the
 * lock screen). Phones without Google Play services have no push to wake
 * the app, so this connection is how a closed app hears of a call.
 *
 * While the app is on screen the app rings instead: it claims the call, and
 * the service holds its notification back until the app releases it. The
 * app keeps the screen on while a call rings, so a screen the user turns off
 * silences the call (the service then shows it without sound), as the power
 * button does for phone calls.
 *
 * With the service off ("Ring when the app is closed" in Settings), the app
 * rings as before: by itself while its process runs, with a heads-up banner
 * while it is in the background.
 */
import { useEffect, useState } from "react";
import { AppState } from "react-native";
import type { CallSession, ChatStore, NotifyEvent } from "@alexmessages/shared";
import { CallRinger, type CallAction } from "../../modules/call-ringer";
import { presentCallNotification, presentMessageNotification, requestOpenChannel } from "../notify";
import { callAudio } from "./callAudio";
import type { CallRoute } from "./route";

/** Answer on the notification opens the app; on a cold start the call
 *  reaches it once the app has signed in and connected. */
const ANSWER_WAIT_MS = 30000;
/** A tap on a ringing call shows the app over the lock screen; if no call
 *  shows up by then, the lock screen comes back. */
const LOCK_WAIT_MS = 15000;

export function attachSystemCalls(store: ChatStore): () => void {
  let pendingAnswer: { callId: string; at: number } | null = null;
  let live = false;
  let lockTimer: ReturnType<typeof setTimeout> | null = null;

  const answerIfRinging = () => {
    const p = pendingAnswer;
    if (!p) return;
    const call = store.state.call;
    if (call?.id === p.callId && call.phase === "incoming") {
      pendingAnswer = null;
      void store.calls.accept();
    } else if (Date.now() - p.at > ANSWER_WAIT_MS) {
      pendingAnswer = null;
    }
  };

  const onAction = (a: CallAction) => {
    if ((a.action === "answer" || a.action === "open") && !live) {
      if (lockTimer) clearTimeout(lockTimer);
      lockTimer = setTimeout(() => {
        lockTimer = null;
        if (!live) void CallRinger.setShowWhenLocked(false);
      }, LOCK_WAIT_MS);
    }
    if (a.action === "answer" && a.callId) {
      pendingAnswer = { callId: a.callId, at: Date.now() };
      answerIfRinging();
    } else if (a.action === "chat" && a.channel) {
      requestOpenChannel(a.channel);
    }
  };

  const sub = CallRinger.addListener("onCallAction", onAction);
  const launch = CallRinger.takeLaunchAction();
  if (launch) onAction(launch);

  const off = store.subscribe(() => {
    answerIfRinging();
    const call = store.state.call;
    const nowLive = !!call && call.phase !== "ended";
    // The call showed over the lock screen; once it ends, the lock screen
    // covers the app again.
    if (live && !nowLive) void CallRinger.setShowWhenLocked(false);
    live = nowLive;
  });
  return () => {
    off();
    sub.remove();
    if (lockTimer) clearTimeout(lockTimer);
  };
}

function useAppActive(): boolean {
  const [active, setActive] = useState(() => AppState.currentState === "active");
  useEffect(() => {
    const sub = AppState.addEventListener("change", (s) => setActive(s === "active"));
    return () => sub.remove();
  }, []);
  return active;
}

export function useCallRoute(call: CallSession | null, ready: boolean): CallRoute {
  const active = useAppActive();
  const id = call?.id ?? null;
  const phase = call?.phase;
  const service = CallRinger.isRunning();
  // With the service running, the app rings only while it is on screen;
  // otherwise the service's notification rings.
  const wanted = ready && phase === "incoming" && !!id && (active || !service);
  const [claim, setClaim] = useState<{ id: string; ring: boolean } | null>(null);
  useEffect(() => {
    if (!wanted || !service || !id) return;
    let stale = false;
    void CallRinger.claim(id).then((ring) => {
      if (!stale) setClaim({ id, ring });
    });
    return () => {
      stale = true;
      setClaim(null);
      void CallRinger.release(id);
    };
  }, [wanted, service, id]);
  const ring = wanted && (!service || (claim?.id === id && claim.ring));

  // The screen stays on while the call rings here (and through the call:
  // callAudio.start does that too), so only the user turns it off.
  const inCall = ready && !!phase && phase !== "incoming" && phase !== "ended";
  const keepOn = (wanted && active) || inCall;
  useEffect(() => {
    if (!keepOn) return;
    callAudio.keepScreenOn(true);
    return () => callAudio.keepScreenOn(false);
  }, [keepOn]);

  return ring ? RING : QUIET;
}

const RING: CallRoute = { ring: true, sounds: callAudio };
const QUIET: CallRoute = { ring: false, sounds: callAudio };

export function useSystemSpeaker(_onChange: (on: boolean) => void): void {}

/** The service posts its own ringing notification while it runs. */
export function notifyIncomingCall(n: NotifyEvent): void {
  if (CallRinger.isRunning()) return;
  void presentCallNotification(n);
}

/** While the app is off screen the service posts the missed call itself. */
export function notifyMissedCall(n: NotifyEvent): void {
  if (CallRinger.isRunning() && AppState.currentState !== "active") return;
  void presentMessageNotification(n);
}

/** A ringing call's tap shows the app over the lock screen; the rest of the
 *  app stays behind an unlock. */
export function lockedOverCall(): boolean {
  return CallRinger.isDeviceLocked();
}

export function unlockToLeave(): Promise<boolean> {
  return CallRinger.requestUnlock();
}
