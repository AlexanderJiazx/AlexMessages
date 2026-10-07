import { nameFor } from "@shared/format";
import { isRemoteUser } from "@shared/matrix";
import { store } from "../client";
import { useChatState } from "../hooks";
import { Icon } from "./icons";
import { MatrixBadge } from "./MatrixBadge";

/** Top bar: hamburger (mobile), conversation title, presence chip. */
export function Topbar({ onMenu }: { onMenu: () => void }) {
  const s = useChatState();
  const peerId = store.peerOf(s.activeChannel);
  const name = s.activeChannel ? nameFor(s.users, peerId) : "Messages";
  const online = peerId != null && s.online.has(peerId);

  return (
    <div className="topbar">
      <button className="menu-btn" aria-label="Open menu" onClick={onMenu}>
        <Icon name="menu" size={18} sw={2} />
      </button>
      <div className="title">
        <h1>
          <span>{name}</span>
        </h1>
        {peerId != null && isRemoteUser(s.users[peerId]) ? (
          // Presence is meaningless for bridged users — the badge replaces it.
          <MatrixBadge user={s.users[peerId]} />
        ) : (
          s.activeChannel && (
            <span className="presence-chip">
              <span className="cdot" style={{ background: online ? "var(--sage)" : "var(--faint)" }} />
              <span>{online ? "online" : "offline"}</span>
            </span>
          )
        )}
      </div>
    </div>
  );
}
