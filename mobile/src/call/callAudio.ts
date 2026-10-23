/** Expo web has no calls (see platform.ts); iOS and Android load callAudio.native.ts. */
import type { CallMedia } from "@alexmessages/shared";

export const callAudio = {
  startRingtone(): void {},
  stopRingtone(): void {},
  start(_media: CallMedia): void {},
  stop(): void {},
  startRingback(): void {},
  stopRingback(): void {},
  setSpeaker(_on: boolean): void {},
  keepScreenOn(_on: boolean): void {},
};
