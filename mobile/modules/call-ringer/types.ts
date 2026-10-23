/** A tap on one of the ring service's notifications. */
export type CallAction = {
  /** "answer" and "open": a ringing call (its Answer button, or its body or
   *  full-screen launch); "chat": a missed call; "app": the "Ready for
   *  calls" notification. */
  action: "answer" | "open" | "chat" | "app";
  callId: string | null;
  channel: string | null;
};

export type CallRingerEvents = {
  onCallAction(e: CallAction): void;
};

/** The Android ring service: calls ring while the app is closed. */
export interface CallRingerApi {
  isRunning(): boolean;
  /** Saves the session (sealed with an Android Keystore key) and starts the
   *  service. socketUrl is the server's /ws URL. Call it in the foreground. */
  start(socketUrl: string, token: string): Promise<void>;
  /** Forgets the session and stops the service. */
  stop(): Promise<void>;
  /** The app rings this call on screen; the service holds its notification
   *  back. Resolves false for a call the user silenced (the screen went off
   *  while it rang): show it without ringing. */
  claim(callId: string): Promise<boolean>;
  /** The app stopped ringing this call. */
  release(callId: string): Promise<void>;
  /** The notification tap that opened the app, within the last minute. */
  takeLaunchAction(): CallAction | null;
  dismissMissed(channel: string): Promise<void>;
  /** The app may post notifications and its "Incoming calls" category is on.
   *  Without both, no call can ring while the app is closed. */
  notificationsAllowed(): boolean;
  /** The app's notification page in the system settings. */
  openNotificationSettings(): Promise<void>;
  /** Android 14+ lets the user deny full-screen call screens. */
  canUseFullScreenIntent(): boolean;
  openFullScreenIntentSettings(): Promise<void>;
  isIgnoringBatteryOptimizations(): boolean;
  requestIgnoreBatteryOptimizations(): Promise<void>;
  /** Shows the app over the lock screen (set by a ringing call's tap). */
  setShowWhenLocked(on: boolean): Promise<void>;
  isDeviceLocked(): boolean;
  /** Asks for the PIN or fingerprint. Resolves true once the phone is unlocked. */
  requestUnlock(): Promise<boolean>;
  addListener<E extends keyof CallRingerEvents>(event: E, listener: CallRingerEvents[E]): { remove(): void };
}
