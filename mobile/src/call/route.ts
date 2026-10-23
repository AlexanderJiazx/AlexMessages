import { callAudio } from "./callAudio";

/** The sounds and audio session of a call (callAudio's shape). */
export type CallSounds = typeof callAudio;

/** How CallHost runs a call's sounds (from system.ts's useCallRoute). */
export interface CallRoute {
  /** Whether the app plays its own ringtone for the ringing call now. */
  ring: boolean;
  /** Who plays the call's sounds and runs its audio session. */
  sounds: CallSounds;
}
