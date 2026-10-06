import { useSyncExternalStore } from "react";
import { store } from "./client";
import type { ChatState } from "@shared/store";

/**
 * Subscribes a component to the ChatStore. `version` bumps on every state
 * change; selectors read from `store.state` inside the render.
 */
export function useStoreVersion(): number {
  return useSyncExternalStore(
    (cb) => store.subscribe(cb),
    () => store.version,
    () => store.version,
  );
}

/** Returns the live chat state; re-renders on every store change. */
export function useChatState(): ChatState {
  useStoreVersion();
  return store.state;
}
