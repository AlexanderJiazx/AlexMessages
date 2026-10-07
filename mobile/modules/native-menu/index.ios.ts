// iOS: the UIKit views from ./ios/NativeMenuModule.swift. The Android/web
// variant (index.ts) is a stub — every call site is gated on Platform.OS.
import { requireNativeView } from "expo";
import type { NativeMenuButtonProps, NativeContextMenuProps } from "./types";

export type { NativeMenuItem, NativeMenuButtonProps, NativeContextMenuProps } from "./types";

export const NativeContextMenu = requireNativeView<NativeContextMenuProps>(
  "NativeMenu",
  "ContextMenuView"
);

export const NativeMenuButton = requireNativeView<NativeMenuButtonProps>(
  "NativeMenu",
  "MenuButtonView"
);
