/**
 * How calls reach the phone's own call handling. Expo web has no calls (see
 * platform.ts), so this file only keeps today's behavior; iOS loads
 * system.ios.ts (CallKit, or LiveCommunicationKit in mainland China) and
 * Android system.android.ts (the ring service: calls ring while the app is
 * closed). All three export the same API.
 */
import type { CallSession, ChatStore, NotifyEvent } from "@alexmessages/shared";
import { presentCallNotification, presentMessageNotification } from "../notify";
import { callAudio } from "./callAudio";
import type { CallRoute } from "./route";

/** Follows the store's call (SessionProvider). Returns the detach. */
export function attachSystemCalls(_store: ChatStore): () => void {
  return () => {};
}

export function useCallRoute(_call: CallSession | null, _ready: boolean): CallRoute {
  return { ring: true, sounds: callAudio };
}

/** The audio route moved on or off the loudspeaker outside the app. */
export function useSystemSpeaker(_onChange: (on: boolean) => void): void {}

/** A call rang while the app was in the background (ChatStore onNotify). */
export function notifyIncomingCall(n: NotifyEvent): void {
  void presentCallNotification(n);
}

/** A call was missed (ChatStore onNotify). */
export function notifyMissedCall(n: NotifyEvent): void {
  void presentMessageNotification(n);
}

/** Whether the call screen shows over the lock screen, so leaving it needs an unlock. */
export function lockedOverCall(): boolean {
  return false;
}

/** Asks for the PIN or fingerprint; true once the phone is unlocked. */
export async function unlockToLeave(): Promise<boolean> {
  return true;
}
