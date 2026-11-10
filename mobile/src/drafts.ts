/**
 * Composer drafts, one per conversation, and the composer work an app
 * reload would lose.
 *
 * Drafts live in memory for the life of the JS runtime, so a thread keeps
 * its unsent text while the user looks at another one. `persistDrafts`
 * writes them to AsyncStorage every time the app leaves the foreground
 * (src/session.tsx) and right before an over-the-air reload
 * (src/updates.ts); the next start reads them back (`restoreDrafts`, called
 * during the session's boot). Sign-out, and every start that ends on the
 * login screen, delete them (`clearDrafts`). A reload waits while a
 * composer records, uploads, or transcribes (`hasComposerWork`), since none
 * of that survives it.
 */
import AsyncStorage from "@react-native-async-storage/async-storage";

const KEY = "drafts_before_reload";

const drafts = new Map<string, string>();
const working = new Set<object>();
// True once `restoreDrafts` has finished. Until then `persistDrafts` writes
// nothing, because a write before the stored drafts were read would erase them.
let restored = false;

export function draftFor(channel: string): string {
  return drafts.get(channel) ?? "";
}

export function saveDraft(channel: string, text: string): void {
  if (text) drafts.set(channel, text);
  else drafts.delete(channel);
}

/** Sign-out: the next account must not see this one's drafts. */
export function clearDrafts(): void {
  drafts.clear();
  void AsyncStorage.removeItem(KEY).catch(() => {});
}

/** A composer marks itself busy (`on`) while it records, uploads, or
 *  transcribes; `token` is any object unique to that composer. */
export function setComposerWork(token: object, on: boolean): void {
  if (on) working.add(token);
  else working.delete(token);
}

export function hasComposerWork(): boolean {
  return working.size > 0;
}

/** Saves the drafts for the next start. Writes nothing until `restoreDrafts`
 *  has finished. With no drafts it removes the stored copy, so a draft that
 *  was deleted or sent does not come back at the next start. */
export async function persistDrafts(): Promise<void> {
  if (!restored) return;
  if (!drafts.size) {
    await AsyncStorage.removeItem(KEY);
    return;
  }
  await AsyncStorage.setItem(KEY, JSON.stringify(Object.fromEntries(drafts)));
}

/** Reads back the stored drafts, once, at app start. A draft already in
 *  memory wins over the stored one. The stored copy stays until the next
 *  `persistDrafts` replaces or removes it. */
export async function restoreDrafts(): Promise<void> {
  try {
    const raw = await AsyncStorage.getItem(KEY);
    if (raw == null) return;
    const saved = JSON.parse(raw) as Record<string, unknown>;
    for (const [channel, text] of Object.entries(saved)) {
      if (typeof text === "string" && text && !drafts.has(channel)) drafts.set(channel, text);
    }
  } catch {
    /* a lost draft never blocks the start */
  } finally {
    restored = true;
  }
}
