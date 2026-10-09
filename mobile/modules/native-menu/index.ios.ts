// iOS: the UIKit views from ./ios/*.swift. The Android/web variant
// (index.ts) is a stub — every call site is gated on Platform.OS.
import { requireNativeModule, requireNativeView } from "expo";
import type { EdgeBlurProps, NativeMenuButtonProps, NativeContextMenuProps } from "./types";

export type {
  NativeMenuItem,
  NativeMenuButtonProps,
  NativeContextMenuProps,
  EdgeBlurProps,
} from "./types";

export const NativeContextMenu = requireNativeView<NativeContextMenuProps>(
  "NativeMenu",
  "ContextMenuView"
);

export const NativeMenuButton = requireNativeView<NativeMenuButtonProps>(
  "NativeMenu",
  "MenuButtonView"
);

/** Gradient-strength edge blur (./ios/EdgeBlurModule.swift). */
export const EdgeBlur = requireNativeView<EdgeBlurProps>("EdgeBlur");

/**
 * Full-screen image viewer (./ios/ImagePreviewModule.swift): flies the tapped
 * image out of its bubble, pinch/double-tap zoom, vertical-drag or Close to
 * dismiss. `sourceTag` is the bubble's React tag (findNodeHandle).
 */
export function previewImage(
  url: string,
  sourceTag: number | null,
  name?: string
): Promise<void> {
  return requireNativeModule("ImagePreview").preview(url, sourceTag, name);
}
