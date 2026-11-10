import { useCallback, useEffect, useLayoutEffect, useRef, useState, type JSX } from "react";
import { formatDate, isoDate, nameFor } from "@shared/format";
import { messageKey, type ChatMessage } from "@shared/store";
import { store } from "../client";
import { useChatState } from "../hooks";
import { CallEntry } from "./CallEntry";
import { MessageBubble } from "./MessageBubble";

/** The first message row visible at the top of the stream: its key and its
 *  bottom edge relative to the stream's top edge. */
function topAnchor(el: HTMLElement | null): { key: string; bottom: number } | null {
  if (!el) return null;
  const top = el.getBoundingClientRect().top;
  for (const row of el.querySelectorAll<HTMLElement>(":scope > .msg[data-key]")) {
    const bottom = row.getBoundingClientRect().bottom - top;
    if (bottom > 0) return { key: row.dataset.key!, bottom };
  }
  return null;
}

/** The bottom edge of the row with `key`, relative to the stream's top. */
function rowOffset(el: HTMLElement, key: string): number | null {
  for (const row of el.querySelectorAll<HTMLElement>(":scope > .msg[data-key]")) {
    if (row.dataset.key === key) return row.getBoundingClientRect().bottom - el.getBoundingClientRect().top;
  }
  return null;
}

/** Within this many pixels of the bottom counts as "at the bottom". */
const STICK_PX = 80;
/** Older history loads once the user scrolls this close to the top. */
const LOAD_OLDER_PX = 120;

/**
 * The scrollable message stream: day separators, lazy history paging at the
 * top, the empty states, and scroll management:
 *  - a switch of conversation starts at the bottom;
 *  - a new message keeps the stream at the bottom only when the user was
 *    already there (or sent it), never when they scrolled up to read;
 *  - an older page prepended at the top keeps the same messages in view.
 */
export function Stream({
  onOpenProfile,
  onImageClick,
}: {
  onOpenProfile: (uid: number) => void;
  onImageClick: (src: string) => void;
}) {
  const s = useChatState();
  const ref = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const prevRef = useRef<{ channel: string | null; firstKey: string | null; lastKey: string | null }>({
    channel: null,
    firstKey: null,
    lastKey: null,
  });
  // The message whose actions are shown by a tap (touch / narrow screens).
  const [revealed, setRevealed] = useState<string | null>(null);

  const channel = s.activeChannel;
  const msgs = (channel ? s.history[channel] : undefined) || [];
  const hasMore = !!(channel && s.historyHasMore[channel]);
  const loading = !!(channel && s.historyLoading[channel]);

  // While an older page is loading, remember which message sits at the top
  // of the view and where (read during render, before the DOM update, like
  // getSnapshotBeforeUpdate), so the view stays on it when the page lands.
  const wasLoadingRef = useRef(false);
  const snap = wasLoadingRef.current ? topAnchor(ref.current) : null;

  const firstKey = msgs.length ? messageKey(msgs[0]) : null;
  const lastKey = msgs.length ? messageKey(msgs[msgs.length - 1]) : null;

  useLayoutEffect(() => {
    const el = ref.current;
    const prev = prevRef.current;
    prevRef.current = { channel, firstKey, lastKey };
    wasLoadingRef.current = loading;
    if (!el) return;
    if (channel !== prev.channel) {
      stickRef.current = true;
      el.scrollTop = el.scrollHeight;
      return;
    }
    const last = msgs[msgs.length - 1];
    const appended = lastKey !== prev.lastKey && lastKey != null;
    const ownSend = appended && last?._status === "sending";
    const prepended =
      firstKey !== prev.firstKey &&
      prev.firstKey != null &&
      msgs.findIndex((m) => messageKey(m) === prev.firstKey) > 0;
    if (stickRef.current || ownSend) {
      el.scrollTop = el.scrollHeight;
      stickRef.current = true;
    } else if (prepended && snap) {
      // Put the anchor message back where it was. Measured on the message
      // itself, so a day separator or name line that disappears above it
      // (it now continues an older run) does not move it. Idempotent: if
      // the browser's own scroll anchoring already corrected, this is 0.
      const now = rowOffset(el, snap.key);
      if (now != null) el.scrollTop += now - snap.bottom;
    }
    // `snap` is read during this same render; msgs identity covers the rest.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [msgs, channel, loading]);

  useEffect(() => setRevealed(null), [channel]);

  /** Asks for the page before the oldest loaded message; true when a
   *  request actually started (the store's own flag guards against
   *  doubles, and is cleared on every outcome, errors included). */
  const loadOlder = useCallback((): boolean => {
    const ch = store.state.activeChannel;
    if (!ch || !store.state.historyHasMore[ch] || store.state.historyLoading[ch]) return false;
    void store.loadOlder(ch);
    return !!store.state.historyLoading[ch];
  }, []);

  // A first page that does not fill the stream shows no scrollbar, so no
  // scroll event would ever ask for older messages: ask after a render.
  // Once per message list, so a failing request is not retried in a loop
  // (the next scroll retries it).
  const autoTriedRef = useRef<ChatMessage[] | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || !hasMore || loading || autoTriedRef.current === msgs) return;
    if ((el.scrollHeight <= el.clientHeight || el.scrollTop < LOAD_OLDER_PX) && loadOlder()) {
      autoTriedRef.current = msgs;
    }
  }, [msgs, hasMore, loading, loadOlder]);

  function onScroll() {
    const el = ref.current;
    if (!el || !channel) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_PX;
    if (el.scrollTop < LOAD_OLDER_PX) loadOlder();
  }

  if (!channel) {
    return (
      <div className="stream" ref={ref}>
        <div className="empty-state">
          <div className="empty-logo" />
          <div className="empty-title serif">No conversation selected</div>
          <div className="empty-sub">
            Pick a conversation from the list, or start a new one with the write button.
          </div>
        </div>
      </div>
    );
  }

  const rows: JSX.Element[] = [];
  if (hasMore) {
    rows.push(
      <div className="history-top" key="history-top">
        <span className="spinner" />
        <span>Loading older messages…</span>
      </div>,
    );
  }
  let lastDate = "";
  let prev: ChatMessage | null = null;
  for (const m of msgs) {
    if (m.type === "system") continue;
    const d = isoDate(m.created_at);
    if (d && d !== lastDate) {
      rows.push(
        <div className="day-sep" key={`sep-${d}`}>
          <span>{formatDate(d)}</span>
        </div>,
      );
      lastDate = d;
    }
    const key = messageKey(m);
    rows.push(
      m.type === "call" ? (
        <CallEntry key={key} m={m} />
      ) : (
        <MessageBubble
          key={key}
          m={m}
          prev={prev}
          allMsgs={msgs}
          onOpenProfile={onOpenProfile}
          onImageClick={onImageClick}
          revealed={revealed === key}
          onReveal={(on) => setRevealed(on ? key : null)}
        />
      ),
    );
    prev = m;
  }

  if (!rows.length && !hasMore && !loading) {
    const peer = store.peerOf(channel);
    return (
      <div className="stream" ref={ref}>
        <div className="empty-state">
          <div className="empty-logo" />
          <div className="empty-title serif">No messages yet</div>
          <div className="empty-sub" dir="auto">
            Say hi to {nameFor(s.users, peer)}.
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      className="stream"
      ref={ref}
      onScroll={onScroll}
      onClick={(e) => {
        // A tap outside every message hides the tapped message's actions.
        if (revealed && !(e.target as HTMLElement).closest(".msg")) setRevealed(null);
      }}
    >
      {rows}
    </div>
  );
}
