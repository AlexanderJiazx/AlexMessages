import type { ColorValue, NativeSyntheticEvent, ViewProps } from "react-native";

/** One UIMenu entry; `id` comes back in `onPressAction`. */
export interface NativeMenuItem {
  id: string;
  title: string;
  /** SF Symbol name. */
  systemImage?: string;
  destructive?: boolean;
}

type ActionEvent = NativeSyntheticEvent<{ id: string }>;

export type NativeContextMenuProps = ViewProps & {
  actions: NativeMenuItem[];
  /** Corner radius of the lifted preview. */
  cornerRadius?: number;
  /** Fill behind the lifted preview (transparent content otherwise shows the shadow through). */
  previewBackgroundColor?: ColorValue;
  onPressAction?: (e: ActionEvent) => void;
};

export type NativeMenuButtonProps = ViewProps & {
  actions: NativeMenuItem[];
  systemImage: string;
  iconSize?: number;
  iconColor?: ColorValue;
  /** VoiceOver label of the button. */
  label?: string;
  onPressAction?: (e: ActionEvent) => void;
};

export type EdgeBlurProps = ViewProps & {
  /** The edge that is blurred most; "top" (default) or "bottom". */
  edge?: "top" | "bottom";
  /** Peak blur radius at the edge. */
  maxRadius?: number;
};
