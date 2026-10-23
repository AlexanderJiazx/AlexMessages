// iOS: CallKit, or LiveCommunicationKit in mainland China (./ios/*.swift).
// The Android/web variant (index.ts) is a stub with kind "none".
import { NativeModule, requireNativeModule } from "expo";
import type { SystemCallKind, SystemCallEnd, SystemCallsApi, SystemCallsEvents } from "./types";

export type { SystemCallKind, SystemCallEnd, SystemCallsApi, SystemCallsEvents } from "./types";

declare class SystemCallsModule extends NativeModule<SystemCallsEvents> {
  readonly kind: SystemCallKind;
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
}

export const SystemCalls: SystemCallsApi = requireNativeModule<SystemCallsModule>("SystemCalls");
