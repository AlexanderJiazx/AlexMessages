import { nameFor } from "@shared/format";
import { store } from "../client";
import { useChatState } from "../hooks";
import { Icon } from "./icons";

/** Top bar: hamburger (mobile), conversation title, presence chip, call buttons. */
export function Topbar({ onMenu }: { onMenu: () => void }) {
  const s = useChatState();
  const peerId = store.peerOf(s.activeChannel);
  const name = s.activeChannel ? nameFor(s.users, peerId) : "Messages";
  const online = peerId != null && s.online.has(peerId);
  const canCall = peerId != null && !!s.users[peerId] && store.calls.supported;
  const inCall = !!s.call && s.call.phase !== "ended";

  return (
    <div className="topbar">
      <button className="menu-btn" aria-label="Open menu" onClick={onMenu}>
        <Icon name="menu" size={18} sw={2} />
      </button>
      <div className="title">
        <h1>
          <span>{name}</span>
        </h1>
        {s.activeChannel && (
          <span className="presence-chip">
            <span className="cdot" style={{ background: online ? "var(--sage)" : "var(--faint)" }} />
            <span>{online ? "online" : "offline"}</span>
          </span>
        )}
      </div>
      {canCall && (
        <div className="call-actions">
          <button
            className="iconbtn"
            type="button"
            aria-label="Start a voice call"
            title="Voice call"
            disabled={inCall}
            onClick={() => store.startCall(peerId, "audio")}
          >
            <Icon name="phone" size={18} sw={1.8} />
          </button>
          <button
            className="iconbtn"
            type="button"
            aria-label="Start a video call"
            title="Video call"
            disabled={inCall}
            onClick={() => store.startCall(peerId, "video")}
          >
            <Icon name="video" size={19} sw={1.8} />
          </button>
        </div>
      )}
    </div>
  );
}
