import { useEffect, useLayoutEffect, useRef, type JSX } from "react";
import { formatDate, isoDate } from "@shared/format";
import { store } from "../client";
import { useChatState } from "../hooks";
import { MessageBubble } from "./MessageBubble";
import type { ChatMessage } from "@shared/store";

/**
 * The scrollable message stream: day separators, lazy history paging at the
 * top, the empty state, and scroll management (stick to bottom on new
 * messages only when the user was already at the bottom — exactly like the
 * legacy client).
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
  const prevCountRef = useRef(0);
  const prevChannelRef = useRef<string | null>(null);
  const loadingOlderRef = useRef(false);
  const prevScrollRef = useRef<{ height: number; top: number } | null>(null);

  const channel = s.activeChannel;
  const msgs = (channel ? s.history[channel] : undefined) || [];

  // Track "near bottom" continuously so async renders (image loads, link
  // previews) can decide whether to keep the stream pinned down.
  const nearBottom = () => {
    const el = ref.current;
    if (!el) return true;
    return el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };

  // Scroll to bottom when: channel switched, or a message was appended while
  // the user was near the bottom. After loading older pages, preserve the
  // visual position instead of jumping.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (channel !== prevChannelRef.current) {
      prevChannelRef.current = channel;
      prevCountRef.current = msgs.length;
      el.scrollTop = el.scrollHeight;
      return;
    }
    if (loadingOlderRef.current && prevScrollRef.current) {
      // Older page prepended — keep the same messages in view.
      el.scrollTop = el.scrollHeight - prevScrollRef.current.height + prevScrollRef.current.top;
      loadingOlderRef.current = false;
      prevScrollRef.current = null;
    } else if (msgs.length !== prevCountRef.current && stickRef.current) {
      el.scrollTop = el.scrollHeight;
    }
    prevCountRef.current = msgs.length;
  }, [msgs, channel]);

  // On channel switch, reset the stickiness to true (start at bottom).
  useEffect(() => {
    stickRef.current = true;
  }, [channel]);

  function onScroll() {
    const el = ref.current;
    if (!el || !channel) return;
    stickRef.current = nearBottom();
    if (el.scrollTop < 120 && s.historyHasMore[channel] && !s.historyLoading[channel]) {
      loadingOlderRef.current = true;
      prevScrollRef.current = { height: el.scrollHeight, top: el.scrollTop };
      void store.loadOlder(channel);
    }
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
  if (s.historyHasMore[channel]) {
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
    rows.push(
      <MessageBubble
        key={m.id + (m.edited_at ?? 0)}
        m={m}
        prev={prev}
        allMsgs={msgs}
        onOpenProfile={onOpenProfile}
        onImageClick={onImageClick}
      />,
    );
    prev = m;
  }

  return (
    <div className="stream" ref={ref} onScroll={onScroll}>
      {rows}
    </div>
  );
}
