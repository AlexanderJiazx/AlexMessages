// Android: the ring service (./android) — calls ring while the app is closed.
// The iOS/web variant (index.ts) is a stub: there is no ring service there.
import { NativeModule, requireNativeModule } from "expo";
import type { CallAction, CallRingerApi, CallRingerEvents } from "./types";

export type { CallAction, CallRingerApi, CallRingerEvents } from "./types";

declare class CallRingerModule extends NativeModule<CallRingerEvents> {
  isRunning(): boolean;
  start(socketUrl: string, token: string): Promise<void>;
  stop(): Promise<void>;
  claim(callId: string): Promise<boolean>;
  release(callId: string): Promise<void>;
  takeLaunchAction(): CallAction | null;
  dismissMissed(channel: string): Promise<void>;
  notificationsAllowed(): boolean;
  openNotificationSettings(): Promise<void>;
  canUseFullScreenIntent(): boolean;
  openFullScreenIntentSettings(): Promise<void>;
  isIgnoringBatteryOptimizations(): boolean;
  requestIgnoreBatteryOptimizations(): Promise<void>;
  setShowWhenLocked(on: boolean): Promise<void>;
  isDeviceLocked(): boolean;
  requestUnlock(): Promise<boolean>;
}

export const hasRingService = true;

export const CallRinger: CallRingerApi = requireNativeModule<CallRingerModule>("CallRinger");
