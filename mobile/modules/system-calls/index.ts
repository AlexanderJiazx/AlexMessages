// Android/web stub: the system call UI is iOS-only (index.ios.ts). With kind
// "none" nothing is ever reported, so these never run.
import type { SystemCallsApi } from "./types";

export type { SystemCallKind, SystemCallEnd, SystemCallsApi, SystemCallsEvents } from "./types";

const done = () => Promise.resolve();

export const SystemCalls: SystemCallsApi = {
  kind: "none",
  reportIncoming: () => Promise.reject(new Error("No system call UI")),
  startOutgoing: () => Promise.reject(new Error("No system call UI")),
  reportConnecting: done,
  reportConnected: done,
  reportEnded: done,
  requestAnswer: () => Promise.reject(new Error("No system call UI")),
  requestEnd: done,
  requestMute: done,
  startRingback: done,
  stopRingback: done,
  setSpeaker: done,
  setInCall: done,
  addListener: () => ({ remove() {} }),
};
