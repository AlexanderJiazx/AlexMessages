import { useEffect, useRef, useState } from "react";
import { nameFor } from "@shared/format";
import { isMatrixID } from "@shared/matrix";
import { api, store, toast } from "../client";
import { useChatState } from "../hooks";
import { Avatar } from "./Avatar";
import { Icon } from "./icons";
import { MatrixBadge } from "./MatrixBadge";
import { enablePushFlow } from "../push";

/** Generic modal shell: dimmed backdrop, .card content, Escape/backdrop close. */
function Modal({
  on,
  onClose,
  className = "",
  children,
}: {
  on: boolean;
  onClose: () => void;
  className?: string;
  children: React.ReactNode;
}) {
  useEffect(() => {
    if (!on) return;
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", esc);
    return () => document.removeEventListener("keydown", esc);
  }, [on, onClose]);
  if (!on) return null;
  return (
    <div
      className={`modal on ${className}`}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      {children}
    </div>
  );
}

// ---------- New-DM modal ----------

export function NewDmModal({ on, onClose }: { on: boolean; onClose: () => void }) {
  const [val, setVal] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (on) {
      setVal("");
      setErr(null);
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [on]);

  async function submit() {
    const res = await store.lookupAndOpen(val);
    if (res.ok) onClose();
    else setErr(res.error);
  }

  return (
    <Modal on={on} onClose={onClose}>
      <div className="card">
        <h3 className="serif">Start a new chat</h3>
        <div
          style={{
            marginBottom: 12,
            color: "var(--muted)",
            fontSize: "12.5px",
            lineHeight: 1.5,
          }}
        >
          Enter the username of the person you want to message.
        </div>
        <input
          ref={inputRef}
          type="text"
          placeholder="username"
          autoCapitalize="off"
          autoComplete="off"
          spellCheck={false}
          value={val}
          onChange={(e) => {
            setVal(e.target.value);
            setErr(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void submit();
            }
          }}
          style={{
            width: "100%",
            padding: "10px 12px",
            border: "1px solid var(--line)",
            borderRadius: 10,
            background: "var(--surface)",
            fontSize: 14,
            outline: "none",
          }}
        />
        {isMatrixID(val) && (
          <div className="mx-offer">
            <Icon name="mail" size={12} sw={1.8} /> Message {val.trim()} on Matrix
          </div>
        )}
        {err && (
          <div style={{ color: "var(--danger)", fontSize: 12, marginTop: 8 }}>{err}</div>
        )}
        <div className="btn-row" style={{ justifyContent: "flex-end", marginTop: 14 }}>
          <button className="btn ghost" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={() => void submit()}>
            Start chat
          </button>
        </div>
      </div>
    </Modal>
  );
}

// ---------- Delete-account modal ----------

export function DeleteAccountModal({ on, onClose }: { on: boolean; onClose: () => void }) {
  const [pw, setPw] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (on) {
      setPw("");
      setErr(null);
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [on]);

  async function confirm() {
    if (!pw) {
      setErr("Enter your password.");
      return;
    }
    try {
      await api.deleteAccount(pw);
      window.location.href = "/login";
    } catch (e) {
      setErr((e as { detail?: string }).detail || "Couldn't delete account.");
    }
  }

  return (
    <Modal on={on} onClose={onClose}>
      <div className="card">
        <h3 className="serif">Delete account</h3>
        <div
          style={{
            color: "var(--muted)",
            fontSize: "12.5px",
            lineHeight: 1.55,
            marginBottom: 14,
          }}
        >
          This permanently deletes your account, profile, and entire message history. This
          cannot be undone. Enter your password to confirm.
        </div>
        <input
          ref={inputRef}
          type="password"
          placeholder="Your password"
          autoComplete="current-password"
          value={pw}
          onChange={(e) => setPw(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void confirm();
            }
          }}
          style={{
            width: "100%",
            padding: "10px 12px",
            border: "1px solid var(--line)",
            borderRadius: 10,
            background: "var(--surface)",
            fontSize: 14,
            outline: "none",
          }}
        />
        {err && <div style={{ color: "var(--danger)", fontSize: 12, marginTop: 8 }}>{err}</div>}
        <div className="btn-row" style={{ justifyContent: "flex-end", marginTop: 14 }}>
          <button className="btn ghost" onClick={onClose}>
            Cancel
          </button>
          <button className="btn danger" onClick={() => void confirm()}>
            Delete forever
          </button>
        </div>
      </div>
    </Modal>
  );
}

// ---------- Peer profile sheet ----------

export function ProfileSheet({
  uid,
  onClose,
}: {
  uid: number | null;
  onClose: () => void;
}) {
  const s = useChatState();
  const u = uid != null ? s.users[uid] : null;
  const online = uid != null && s.online.has(uid);
  const isContact = uid != null && s.contacts.has(uid);

  return (
    <Modal on={uid != null} onClose={onClose}>
      <div className="card sheet-card">
        <div className="detail-hd">
          <b>Profile</b>
          <button className="iconbtn" aria-label="Close" title="Close" onClick={onClose}>
            <Icon name="x" size={14} sw={2} />
          </button>
        </div>
        <div className="detail-body">
          <div className="profile-banner" />
          <div className="profile-row">
            <Avatar uid={uid} large />
            <div className="name-block">
              <div className="name">
                {u?.display_name || nameFor(s.users, uid)}
                <MatrixBadge user={u} />
              </div>
            </div>
          </div>
          <div className="field">
            <label>Bio</label>
            <div className={`read-only${u?.bio ? "" : " empty"}`}>
              {u?.bio || "No bio yet."}
            </div>
          </div>
          <div className="btn-row">
            <button
              className="btn primary"
              onClick={() => {
                if (uid != null) {
                  onClose();
                  store.openDM(uid);
                }
              }}
            >
              <Icon name="mail" size={13} sw={1.8} /> Message
            </button>
            {uid != null && (
              <button className="btn" onClick={() => void store.toggleContact(uid)}>
                {isContact ? "Remove from contacts" : "Add to contacts"}
              </button>
            )}
          </div>
          <div className="field" style={{ marginTop: 18 }}>
            <label>Account</label>
            <div className="net-card">
              <div className="row">
                <span className="k">Username</span>
                <span className="v">@{u?.username || "?"}</span>
              </div>
              <div className="row">
                <span className="k">User ID</span>
                <span className="v">#{uid ?? "?"}</span>
              </div>
              {u?.matrix_id && (
                <div className="row">
                  <span className="k">Network</span>
                  <span className="v">Matrix</span>
                </div>
              )}
              <div className="row">
                <span className="k">Status</span>
                <span
                  className="v"
                  style={{
                    color: online || u?.matrix_id ? "var(--sage-deep)" : "var(--muted)",
                  }}
                >
                  {u?.matrix_id ? "on Matrix" : online ? "online" : "offline"}
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}

// ---------- Push opt-in modal ----------

export function PushPromptModal({ on, onClose }: { on: boolean; onClose: () => void }) {
  return (
    <Modal on={on} onClose={onClose}>
      <div className="card">
        <div className="push-illus" aria-hidden="true">
          <Icon name="bell" size={34} sw={1.6} />
        </div>
        <h3 className="serif">Stay in the loop</h3>
        <div
          style={{
            color: "var(--muted)",
            fontSize: 13,
            lineHeight: 1.55,
            marginBottom: 16,
          }}
        >
          Get a notification when someone sends you a direct message, even when Alex
          Messages isn't open. You can change this anytime in your browser settings.
        </div>
        <div className="btn-row" style={{ justifyContent: "flex-end" }}>
          <button
            className="btn ghost"
            onClick={() => {
              try {
                localStorage.setItem("am_push_declined", "1");
              } catch {
                /* private mode */
              }
              onClose();
            }}
          >
            Not now
          </button>
          <button
            className="btn primary"
            onClick={() => {
              onClose();
              void enablePushFlow();
            }}
          >
            Enable notifications
          </button>
        </div>
      </div>
    </Modal>
  );
}
