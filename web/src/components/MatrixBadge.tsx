import { isRemoteUser } from "@shared/matrix";
import type { PublicUser } from "@shared/types";

/** Small "Matrix" pill marking a bridged remote user. Null for local users. */
export function MatrixBadge({ user }: { user: PublicUser | null | undefined }) {
  if (!isRemoteUser(user)) return null;
  return (
    <span className="mx-badge" title={user?.matrix_id ?? "Matrix"}>
      Matrix
    </span>
  );
}
