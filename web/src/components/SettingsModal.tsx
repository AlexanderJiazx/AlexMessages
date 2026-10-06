import { useEffect, useRef, useState } from "react";
import { fmtAccountDate } from "@shared/format";
import { api, store, toast } from "../client";
import { useChatState } from "../hooks";
import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./icons";
import { enablePushFlow, disablePushFlow, pushSupported } from "../push";

type TabId = "profile" | "account" | "notifications" | "data" | "admin";

/** The Apple-System-Settings-style overlay with vertical tabs. */
export function SettingsModal({
  open,
  onClose,
  onOpenDelete,
}: {
  open: boolean;
  onClose: () => void;
  onOpenDelete: () => void;
}) {
  const s = useChatState();
  const [tab, setTab] = useState<TabId>("profile");
  const [avatarMenu, setAvatarMenu] = useState(false);
  const avatarInputRef = useRef<HTMLInputElement>(null);
  const camRef = useRef<HTMLButtonElement>(null);

  const tabs: { id: TabId; label: string; icon: IconName }[] = [
    { id: "profile", label: "Profile", icon: "user" },
    { id: "account", label: "Account", icon: "shield" },
    { id: "notifications", label: "Notifications", icon: "bell" },
    { id: "data", label: "Data", icon: "database" },
    ...(s.isAdmin ? [{ id: "admin" as TabId, label: "Admin", icon: "lock" as IconName }] : []),
  ];

  useEffect(() => {
    if (!open) return;
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", esc);
    return () => document.removeEventListener("keydown", esc);
  }, [open, onClose]);

  useEffect(() => {
    if (!avatarMenu) return;
    const away = (e: MouseEvent) => {
      const t = e.target as HTMLElement;
      if (!t.closest("#avatarMenu") && !t.closest(".avatar-cam")) setAvatarMenu(false);
    };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [avatarMenu]);

  async function onAvatarFile(file: File | undefined) {
    if (!file) return;
    if (file.size > 5 * 1024 * 1024) {
      toast("Photo exceeds 5MB", true);
      return;
    }
    try {
      const j = await api.setAvatar(file);
      store.applyOwnProfile(j.user);
      toast("Photo updated");
    } catch (e) {
      toast((e as { detail?: string }).detail || "Couldn't update photo", true);
    }
  }

  async function removeAvatar() {
    try {
      const j = await api.deleteAvatar();
      store.applyOwnProfile(j.user);
      toast("Photo removed");
    } catch {
      toast("Couldn't remove photo", true);
    }
  }

  if (!open || !s.me) return null;
  const me = s.me;

  return (
    <div className="modal settings-modal on" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="settings-card">
        <button className="settings-close iconbtn" aria-label="Close" title="Close" onClick={onClose}>
          <Icon name="x" size={14} sw={2} />
        </button>
        <nav className="settings-nav">
          <div className="settings-nav-title serif">Settings</div>
          <div className="settings-tabs">
            {tabs.map((t) => (
              <button
                key={t.id}
                className={`settings-tab${t.id === tab ? " active" : ""}`}
                onClick={() => setTab(t.id)}
              >
                <Icon name={t.icon} size={17} />
                <span>{t.label}</span>
              </button>
            ))}
          </div>
        </nav>
        <div className="settings-main">
          <div className="settings-content">
            {tab === "profile" && (
              <ProfileTab
                camRef={camRef}
                onCamClick={() => {
                  if (!me.avatar) avatarInputRef.current?.click();
                  else setAvatarMenu((v) => !v);
                }}
              />
            )}
            {tab === "account" && <AccountTab onOpenDelete={onOpenDelete} />}
            {tab === "notifications" && <NotificationsTab />}
            {tab === "data" && <DataTab />}
            {tab === "admin" && <AdminTab />}
          </div>
        </div>
      </div>

      {avatarMenu && camRef.current && (
        <AvatarMenu
          anchor={camRef.current}
          onUpload={() => {
            setAvatarMenu(false);
            avatarInputRef.current?.click();
          }}
          onRemove={() => {
            setAvatarMenu(false);
            void removeAvatar();
          }}
        />
      )}
      <input
        ref={avatarInputRef}
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp"
        style={{ display: "none" }}
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          void onAvatarFile(f);
        }}
      />
    </div>
  );
}

/** Avatar upload/remove popup menu anchored to the camera button. */
function AvatarMenu({
  anchor,
  onUpload,
  onRemove,
}: {
  anchor: HTMLElement;
  onUpload: () => void;
  onRemove: () => void;
}) {
  const r = anchor.getBoundingClientRect();
  let left = r.right - 170;
  if (left < 8) left = 8;
  let top = r.bottom + 8;
  return (
    <div
      id="avatarMenu"
      className="avatar-menu"
      style={{ position: "fixed", left, top }}
      ref={(el) => {
        if (!el) return;
        const mr = el.getBoundingClientRect();
        if (mr.bottom > window.innerHeight - 8) el.style.top = `${r.top - mr.height - 8}px`;
      }}
    >
      <button onClick={onUpload}>
        <Icon name="upload" size={15} />
        <span>Upload photo</span>
      </button>
      <button className="danger" onClick={onRemove}>
        <Icon name="trash" size={15} />
        <span>Remove photo</span>
      </button>
    </div>
  );
}

// ---------- Profile tab ----------

function ProfileTab({
  onCamClick,
  camRef,
}: {
  onCamClick: () => void;
  camRef: React.RefObject<HTMLButtonElement>;
}) {
  const s = useChatState();
  const me = s.me!;
  const [name, setName] = useState(me.display_name || "");
  const [bio, setBio] = useState(me.bio || "");
  const bioRef = useRef<HTMLTextAreaElement>(null);
  const dirty = name.trim() !== (me.display_name || "") || bio.trim() !== (me.bio || "");

  useEffect(() => {
    const el = bioRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(220, el.scrollHeight) + "px";
  }, [bio]);

  async function save() {
    const ok = await store.saveProfile(name.trim(), bio.trim());
    if (ok) {
      setName(store.state.me?.display_name || "");
      setBio(store.state.me?.bio || "");
    }
  }

  return (
    <>
      <h2 className="settings-h">Profile</h2>
      <div className="avatar-editor">
        <Avatar uid={me.id} large />
        <button
          ref={camRef}
          className="avatar-cam"
          aria-label="Change photo"
          title="Change photo"
          onClick={onCamClick}
        >
          <Icon name="camera" size={16} />
        </button>
      </div>
      <input
        className="profile-name-input"
        maxLength={40}
        value={name}
        placeholder="Your name"
        onChange={(e) => setName(e.target.value)}
      />
      <div className="settings-group" style={{ marginTop: 18 }}>
        <div className="settings-group-label">Bio</div>
        <textarea
          ref={bioRef}
          className="bio"
          maxLength={280}
          placeholder="A line or two about you…"
          value={bio}
          onChange={(e) => setBio(e.target.value)}
        />
      </div>
      <div className={`settings-save-bar${dirty ? " on" : ""}`}>
        <button
          className="btn ghost"
          onClick={() => {
            setName(me.display_name || "");
            setBio(me.bio || "");
          }}
        >
          Cancel
        </button>
        <button className="btn primary" onClick={() => void save()}>
          Save changes
        </button>
      </div>
    </>
  );
}

// ---------- Account tab ----------

function AccountTab({ onOpenDelete }: { onOpenDelete: () => void }) {
  const s = useChatState();
  const me = s.me!;
  const [pw, setPw] = useState({ cur: "", next: "", confirm: "" });
  const [pwMsg, setPwMsg] = useState<{ text: string; ok: boolean } | null>(null);

  async function submitPassword() {
    if (pw.next.length < 8 || pw.next.length > 128) {
      setPwMsg({ text: "New password must be 8–128 characters.", ok: false });
      return;
    }
    if (pw.next !== pw.confirm) {
      setPwMsg({ text: "New passwords don't match.", ok: false });
      return;
    }
    try {
      await api.changePassword(pw.cur, pw.next);
      setPw({ cur: "", next: "", confirm: "" });
      setPwMsg({ text: "Password updated. Other devices were signed out.", ok: true });
      toast("Password updated");
    } catch (e) {
      setPwMsg({ text: (e as { detail?: string }).detail || "Couldn't update password.", ok: false });
    }
  }

  async function logout() {
    try {
      await api.logout();
    } catch {
      /* navigate anyway */
    }
    window.location.href = "/login";
  }

  return (
    <>
      <h2 className="settings-h">Account</h2>
      <div className="settings-group">
        <div className="settings-group-label">Account info</div>
        <div className="net-card">
          <div className="row">
            <span className="k">Username</span>
            <span className="v">@{me.username}</span>
          </div>
          <div className="row">
            <span className="k">User ID</span>
            <span className="v">#{me.id}</span>
          </div>
          <div className="row">
            <span className="k">Member since</span>
            <span className="v">{fmtAccountDate(s.meCreatedAt)}</span>
          </div>
          <div className="row">
            <span className="k">Status</span>
            <span className="v" style={{ color: "var(--sage-deep)" }}>
              approved
            </span>
          </div>
        </div>
      </div>
      <div className="settings-group">
        <div className="settings-group-label">Change password</div>
        <div className="pw-form">
          <input
            type="password"
            placeholder="Current password"
            autoComplete="current-password"
            value={pw.cur}
            onChange={(e) => setPw({ ...pw, cur: e.target.value })}
          />
          <input
            type="password"
            placeholder="New password (8–128 chars)"
            autoComplete="new-password"
            value={pw.next}
            onChange={(e) => setPw({ ...pw, next: e.target.value })}
          />
          <input
            type="password"
            placeholder="Confirm new password"
            autoComplete="new-password"
            value={pw.confirm}
            onChange={(e) => setPw({ ...pw, confirm: e.target.value })}
          />
          {pwMsg && (
            <div
              className="settings-sub"
              style={{ margin: "2px 0 0", color: pwMsg.ok ? "var(--sage-deep)" : "var(--danger)" }}
            >
              {pwMsg.text}
            </div>
          )}
          <div className="btn-row">
            <button className="btn primary" onClick={() => void submitPassword()}>
              Update password
            </button>
          </div>
        </div>
      </div>
      <div className="settings-group">
        <div className="settings-group-label">Session</div>
        <SettingsRow
          title="Log out from all of your devices"
          desc="Ends every active session, including this one."
          control={
            <button className="btn" onClick={() => void logout()}>
              <Icon name="logout" size={14} /> Log out
            </button>
          }
        />
      </div>
      <div className="settings-group">
        <div className="settings-group-label">Danger zone</div>
        <div className="danger-zone">
          <div className="dz-title">Delete account</div>
          <div className="dz-desc">
            Permanently deletes your account, profile, and message history. This can't be undone.
          </div>
          <button className="btn danger" onClick={onOpenDelete}>
            Delete my account…
          </button>
        </div>
      </div>
    </>
  );
}

// ---------- Notifications tab ----------

function NotificationsTab() {
  const s = useChatState();
  if (!pushSupported()) {
    return (
      <>
        <h2 className="settings-h">Notifications</h2>
        <SettingsRow
          title="Direct message alerts"
          desc="Push notifications aren't supported in this browser."
          control={null}
        />
      </>
    );
  }
  const perm = Notification.permission;
  const enabled = s.pushSubscribed && perm === "granted";
  let desc: string;
  const disabled = perm === "denied";
  if (perm === "denied") {
    desc =
      "Blocked by your browser. Open the site settings (click the lock icon in the address bar) to allow notifications.";
  } else if (enabled) {
    desc =
      "You'll get a system notification for new direct messages while Alex Messages isn't open or focused.";
  } else {
    desc =
      "Get a system notification for new direct messages even when Alex Messages is in the background.";
  }
  return (
    <>
      <h2 className="settings-h">Notifications</h2>
      <SettingsRow
        title="Direct message alerts"
        desc={desc}
        control={
          <button
            className={`toggle${enabled ? " on" : ""}`}
            role="switch"
            aria-checked={enabled}
            aria-label="Toggle notifications"
            disabled={disabled}
            onClick={() => {
              if (enabled) void disablePushFlow();
              else void enablePushFlow();
            }}
          />
        }
      />
    </>
  );
}

// ---------- Data tab ----------

function DataTab() {
  return (
    <>
      <h2 className="settings-h">Data</h2>
      <SettingsRow
        title="Export my data"
        desc="Download a copy of your account, contacts, and full message history as a JSON file."
        control={
          <a className="btn" href="/api/me/export" download>
            <Icon name="download" size={14} /> Export
          </a>
        }
      />
    </>
  );
}

// ---------- Admin tab ----------

function AdminTab() {
  const port = "8001"; // admin server default (dev mapping from the legacy client)
  const url = `${location.protocol}//${location.hostname}:${port}/`;
  return (
    <>
      <h2 className="settings-h">Admin</h2>
      <div className="settings-sub" style={{ marginTop: 0 }}>
        You have administrator access. The control panel opens in a new tab.
      </div>
      <div className="btn-row">
        <a className="btn primary" href={url} target="_blank" rel="noopener">
          <Icon name="external" size={14} /> Open admin panel
        </a>
      </div>
    </>
  );
}

// ---------- shared bits ----------

/** Label-left / control-right settings row (the shared layout from app.js). */
export function SettingsRow({
  title,
  desc,
  control,
}: {
  title: string;
  desc?: string;
  control: React.ReactNode;
}) {
  return (
    <div className="settings-row">
      <div className="settings-row-label">
        <div className="t">{title}</div>
        {desc && <div className="d">{desc}</div>}
      </div>
      {control}
    </div>
  );
}
