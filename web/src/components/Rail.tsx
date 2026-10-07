import { useEffect, useRef, useState } from "react";
import { nameFor, lastMessagePreviewFor } from "@shared/format";
import { store } from "../client";
import { useChatState } from "../hooks";
import { Avatar } from "./Avatar";
import { MatrixBadge } from "./MatrixBadge";
import { Icon } from "./icons";

/** Left rail: "Messages" title, compose button, pinned + normal DM list,
 *  and the bottom account trigger that opens Settings. */
export function Rail({
  open,
  onClose,
  onNewDm,
  onOpenSettings,
}: {
  open: boolean;
  onClose: () => void;
  onNewDm: () => void;
  onOpenSettings: () => void;
}) {
  const s = useChatState();
  const items = store.dmListItems();
  const pinned = items.filter((it) => store.dmStateFor(it.channel).pinned);
  const normal = items.filter((it) => !store.dmStateFor(it.channel).pinned);
  const me = s.me;

  return (
    <aside className={`pane left${open ? " open" : ""}`} id="leftPane">
      <div className="rail-hd">
        <h1 className="rail-title serif">Messages</h1>
        <button
          className="iconbtn compose-btn"
          title="New conversation"
          aria-label="New conversation"
          onClick={onNewDm}
        >
          <Icon name="compose" size={18} sw={1.8} />
        </button>
        <button className="rail-close" aria-label="Close" onClick={onClose}>
          ✕
        </button>
      </div>

      <div className="dms-scroll">
        {pinned.length > 0 && (
          <div>
            <div className="rail-section">
              <span>Pinned</span>
            </div>
            <div className="dms">
              {pinned.map((it) => (
                <DmRow key={it.channel} item={it} onClose={onClose} />
              ))}
            </div>
          </div>
        )}
        <div className="dms">
          {normal.map((it) => (
            <DmRow key={it.channel} item={it} onClose={onClose} />
          ))}
        </div>
      </div>

      <button
        className="rail-account"
        aria-label="Settings"
        title="Settings"
        onClick={onOpenSettings}
      >
        <span className="rail-account-av">
          <Avatar uid={me?.id} />
        </span>
        <span className="rail-account-meta">
          <b>{me ? me.display_name || me.username : "—"}</b>
          <span>{me ? `@${me.username}` : ""}</span>
        </span>
        <span className="rail-account-cog" aria-hidden="true">
          <Icon name="cog" size={16} />
        </span>
      </button>
    </aside>
  );
}

function DmRow({
  item,
  onClose,
}: {
  item: { channel: string; peerId: number };
  onClose: () => void;
}) {
  const s = useChatState();
  const [menuOpen, setMenuOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement>(null);
  const isActive = item.channel === s.activeChannel;
  const unread = store.isUnread(item.channel);
  const preview = lastMessagePreviewFor(s.history[item.channel], s.me?.id ?? null);

  return (
    <div className={`dm-row${isActive ? " active" : ""}${unread ? " unread" : ""}`}>
      <button
        className="dm"
        onClick={() => {
          store.switchChannel(item.channel);
          onClose();
        }}
      >
        <Avatar uid={item.peerId} />
        <div className="who">
          <b>
            {nameFor(s.users, item.peerId)}
            <MatrixBadge user={s.users[item.peerId]} />
          </b>
          <span className="sub">{preview}</span>
        </div>
        <span
          className="unread-dot"
          title="Unread messages"
          aria-hidden={unread ? "false" : "true"}
        />
      </button>
      <button
        ref={btnRef}
        className="dm-menu-btn"
        aria-label="Conversation actions"
        title="Actions"
        onClick={(e) => {
          e.stopPropagation();
          setMenuOpen((v) => !v);
        }}
      >
        <Icon name="dots" size={14} sw={2.2} />
      </button>
      {menuOpen && (
        <DmMenu channel={item.channel} anchor={btnRef.current} onClose={() => setMenuOpen(false)} />
      )}
    </div>
  );
}

/** The three-dot conversation menu — floats near its anchor button. */
function DmMenu({
  channel,
  anchor,
  onClose,
}: {
  channel: string;
  anchor: HTMLButtonElement | null;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const st = store.dmStateFor(channel);
  const unread = store.isUnread(channel);

  useEffect(() => {
    const el = ref.current;
    if (!el || !anchor) return;
    const r = anchor.getBoundingClientRect();
    el.style.position = "fixed";
    let left = r.right - 180;
    if (left < 8) left = 8;
    el.style.left = `${left}px`;
    el.style.top = `${r.bottom + 8}px`;
    const mr = el.getBoundingClientRect();
    if (mr.bottom > window.innerHeight - 8) {
      el.style.top = `${r.top - mr.height - 8}px`;
    }
    const away = (e: MouseEvent) => {
      if (!el.contains(e.target as Node)) onClose();
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    window.addEventListener("resize", onClose);
    document.addEventListener("scroll", onClose, true);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
      window.removeEventListener("resize", onClose);
      document.removeEventListener("scroll", onClose, true);
    };
  }, [anchor, onClose]);

  return (
    <div className="dm-menu" ref={ref}>
      <button
        onClick={() => {
          onClose();
          void store.togglePin(channel, !st.pinned);
        }}
      >
        {st.pinned ? "Unpin" : "Pin"} conversation
      </button>
      <button
        onClick={() => {
          onClose();
          if (unread) void store.markRead(channel);
          else void store.markUnread(channel);
        }}
      >
        {unread ? "Mark as read" : "Mark as unread"}
      </button>
      <button
        className="danger"
        onClick={() => {
          onClose();
          void store.deleteDm(channel);
        }}
      >
        Delete for me
      </button>
    </div>
  );
}
