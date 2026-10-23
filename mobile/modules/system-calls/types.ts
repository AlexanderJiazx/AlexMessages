/** Which system call UI this iPhone uses: "none" off iOS, on iOS before
 *  17.4 in mainland China (no CallKit there), and in the iOS Simulator (no
 *  system call UI there). */
export type SystemCallKind = "callkit" | "livecommunication" | "none";

/** Why a call ended, as the system call UI records it. */
export type SystemCallEnd = "failed" | "remoteEnded" | "unanswered" | "answeredElsewhere" | "declinedElsewhere";

/** What the user did in the system call UI (not in the app). */
export type SystemCallsEvents = {
  onAnswer(e: { key: number }): void;
  onEnd(e: { key: number }): void;
  onMute(e: { key: number; muted: boolean }): void;
  /** The system dropped every call. */
  onReset(): void;
  /** The audio route moved on or off the loudspeaker outside the app. */
  onSpeaker(e: { on: boolean }): void;
};

/** Calls are named by their CallSession key. */
export interface SystemCallsApi {
  readonly kind: SystemCallKind;
  /** Rejects with code ERR_FILTERED (Do Not Disturb, a Focus, the block
   *  list) or ERR_REFUSED: the app then rings by itself, or stays silent. */
  reportIncoming(key: number, handle: string, name: string, video: boolean): Promise<void>;
  startOutgoing(key: number, handle: string, name: string, video: boolean): Promise<void>;
  reportConnecting(key: number): Promise<void>;
  reportConnected(key: number): Promise<void>;
  reportEnded(key: number, reason: SystemCallEnd): Promise<void>;
  requestAnswer(key: number): Promise<void>;
  requestEnd(key: number): Promise<void>;
  requestMute(key: number, muted: boolean): Promise<void>;
  startRingback(): Promise<void>;
  stopRingback(): Promise<void>;
  setSpeaker(on: boolean): Promise<void>;
  setInCall(on: boolean): Promise<void>;
  addListener<E extends keyof SystemCallsEvents>(event: E, listener: SystemCallsEvents[E]): { remove(): void };
}
