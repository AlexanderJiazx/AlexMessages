import { colorFor, initialsFor, nameFor, shade } from "@shared/format";
import { isRemoteUser } from "@shared/matrix";
import { store } from "../client";
import { useStoreVersion } from "../hooks";

/**
 * The generated-avatar block used across the app: gradient circle with
 * initials (or the uploaded photo) plus the online-status dot.
 */
export function Avatar({
  uid,
  large = false,
  showStatus = true,
  onClick,
}: {
  uid: number | null | undefined;
  large?: boolean;
  showStatus?: boolean;
  onClick?: () => void;
}) {
  useStoreVersion();
  const s = store.state;
  const c = colorFor(uid);
  const online = uid != null && s.online.has(uid);
  const u = uid != null ? s.users[uid] : undefined;
  return (
    <div
      className={large ? "av lg" : "av"}
      style={{ background: `linear-gradient(135deg, ${c}, ${shade(c, -0.18)})` }}
      title={nameFor(s.users, uid)}
      onClick={onClick}
      role={onClick ? "button" : undefined}
    >
      {u?.avatar ? (
        <img className="av-photo" src={u.avatar} alt="" loading="lazy" />
      ) : (
        <span>{initialsFor(s.users, uid)}</span>
      )}
      {/* Presence is meaningless for bridged users — no dot. */}
      {showStatus && !isRemoteUser(u) && (
        <span className={online ? "stat" : "stat offline"} />
      )}
    </div>
  );
}
