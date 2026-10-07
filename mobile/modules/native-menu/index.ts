// Android/web stub: the native views are iOS-only and every call site is
// gated on Platform.OS === "ios", so these never render — they exist only so
// the module resolves.
import type React from "react";
import type { NativeMenuButtonProps, NativeContextMenuProps } from "./types";

export type { NativeMenuItem, NativeMenuButtonProps, NativeContextMenuProps } from "./types";

const Stub = () => null;

export const NativeContextMenu = Stub as unknown as React.ComponentType<NativeContextMenuProps>;
export const NativeMenuButton = Stub as unknown as React.ComponentType<NativeMenuButtonProps>;
