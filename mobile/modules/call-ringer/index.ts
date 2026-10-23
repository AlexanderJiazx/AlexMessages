// iOS/web stub: the ring service is Android-only (index.android.ts). With
// hasRingService false the app never starts it, so these never run.
import type { CallRingerApi } from "./types";

export type { CallAction, CallRingerApi, CallRingerEvents } from "./types";

const done = () => Promise.resolve();

export const hasRingService = false;

export const CallRinger: CallRingerApi = {
  isRunning: () => false,
  start: done,
  stop: done,
  claim: () => Promise.resolve(true),
  release: done,
  takeLaunchAction: () => null,
  dismissMissed: done,
  notificationsAllowed: () => true,
  openNotificationSettings: done,
  canUseFullScreenIntent: () => true,
  openFullScreenIntentSettings: done,
  isIgnoringBatteryOptimizations: () => true,
  requestIgnoreBatteryOptimizations: done,
  setShowWhenLocked: done,
  isDeviceLocked: () => false,
  requestUnlock: () => Promise.resolve(true),
  addListener: () => ({ remove() {} }),
};
