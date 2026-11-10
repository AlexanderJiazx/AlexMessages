import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import * as Updates from "expo-updates";
import type { ChatStore } from "@alexmessages/shared";
import { hasComposerWork, persistDrafts } from "./drafts";

/** How long the app must have been away before a downloaded update applies. */
const AWAY_MS = 15 * 60 * 1000;

/**
 * Over-the-air updates (expo-updates, see the server's
 * internal/webapp/ota_routes.go). Every app start asks the server for the
 * newest update and downloads it in the background; it runs from the next
 * start. This hook also applies a downloaded update when the app comes back
 * after at least 15 minutes away, so a reload never cuts into a
 * conversation, and checks the server again at that moment. It never reloads
 * during a call or while a composer records, uploads, or holds staged files;
 * composer drafts are saved across the reload (src/drafts.ts).
 */
export function useUpdatesOnReturn(store: ChatStore): void {
  const { isUpdatePending } = Updates.useUpdates();
  const pending = useRef(isUpdatePending);
  pending.current = isUpdatePending;

  useEffect(() => {
    if (!Updates.isEnabled) return;
    let awaySince = 0;
    const sub = AppState.addEventListener("change", (s) => {
      if (s === "background") {
        awaySince = Date.now();
        return;
      }
      if (s !== "active" || !awaySince) return;
      const away = Date.now() - awaySince;
      awaySince = 0;
      if (away < AWAY_MS) return;
      // Never during a call, and never over composer work a reload would
      // lose (a recording, an upload, staged files); drafts are saved and
      // come back after the reload (src/drafts.ts). A skipped update
      // applies at the next return or start.
      if (pending.current && !store.state.call && !hasComposerWork() && !store.state.pendingAtt.length) {
        void persistDrafts()
          .catch(() => {})
          .then(() => Updates.reloadAsync())
          .catch(() => {});
        return;
      }
      // Downloaded now, it applies at the next return or start.
      void Updates.checkForUpdateAsync()
        .then((r) => (r.isAvailable ? Updates.fetchUpdateAsync() : null))
        .catch(() => {});
    });
    return () => sub.remove();
  }, [store]);
}

/** Which code this app runs, for the Settings footer. */
export function runningCodeLabel(): string {
  if (!Updates.isEnabled) return "development build";
  if (Updates.isEmbeddedLaunch || !Updates.createdAt) return "built-in code";
  const when = Updates.createdAt.toLocaleString(undefined, {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
  return `update of ${when}`;
}
