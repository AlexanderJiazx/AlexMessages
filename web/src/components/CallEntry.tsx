import { callEntryText, fmtTime } from "@shared/format";
import type { ChatMessage } from "@shared/store";
import { store } from "../client";
import { useChatState } from "../hooks";
import { Icon } from "./icons";

/**
 * A finished call in the thread ("Video call · 4:32", "Missed voice call"),
 * on the caller's side like a message, with a button to call back.
 */
export function CallEntry({ m }: { m: ChatMessage }) {
  const s = useChatState();
  const call = m.call;
  if (!call) return null;
  const t = callEntryText(call, s.me?.id);
  const peerId = store.peerOf(m.channel);
  const inCall = !!s.call && s.call.phase !== "ended";
  const again = t.outgoing ? "Call again" : "Call back";

  return (
    <div className={`call-entry ${t.outgoing ? "me" : "them"}${t.missed ? " missed" : ""}`} id={`msg-${m.id}`}>
      <div className="call-entry-card">
        <span className="call-entry-ico">
          <Icon name={call.media === "video" ? "video" : "phone"} size={17} sw={1.9} />
        </span>
        <span className="call-entry-text">
          <b>{t.title}</b>
          <span>{[t.detail, fmtTime(call.started_at)].filter(Boolean).join(" · ")}</span>
        </span>
        {store.calls.supported && peerId != null && (
          <button
            className="call-entry-again"
            type="button"
            disabled={inCall}
            aria-label={again}
            title={again}
            onClick={() => store.startCall(peerId, call.media)}
          >
            <Icon name={call.media === "video" ? "video" : "phone"} size={15} sw={1.9} />
          </button>
        )}
      </div>
    </div>
  );
}
