import React from "react";
import { Platform, Pressable, View, type ColorValue, type StyleProp, type ViewStyle } from "react-native";
import { Button, Host, Menu, RNHostView, buttonStyle, menuIndicator } from "./SwiftUI";
import type { SFSymbol } from "sf-symbols-typescript";
import { NativeContextMenu, NativeMenuButton, type NativeMenuItem } from "../../modules/native-menu";
import { colors, glassSupported } from "../theme";
import { showActionSheet } from "./ActionSheet";
import { GlassIconButton } from "./Glass";
import type { IconName } from "./Icon";

/** One menu entry — rendered as a native UIMenu item on iOS. */
export interface MenuAction {
  label: string;
  /** SF Symbol shown beside the label on iOS. */
  systemImage?: SFSymbol;
  destructive?: boolean;
  onPress: () => void;
}

/** UIKit context menus work on every supported iOS; Android uses ActionSheet. */
const nativeContextMenus = Platform.OS === "ios";
/** The glass menu button (and SwiftUI pull-downs) need iOS 26. */
const nativeMenus = Platform.OS === "ios" && glassSupported;

/** Menu entries for the UIKit views; the index is the action id. */
function toItems(actions: MenuAction[]): NativeMenuItem[] {
  return actions.map((a, i) => ({
    id: String(i),
    title: a.label,
    systemImage: a.systemImage,
    destructive: a.destructive,
  }));
}

function pressById(actions: MenuAction[], id: string) {
  actions[Number(id)]?.onPress();
}

function MenuItems({ actions }: { actions: MenuAction[] }) {
  return (
    <>
      {actions.map((a) => (
        <Button
          key={a.label}
          label={a.label}
          systemImage={a.systemImage}
          role={a.destructive ? "destructive" : undefined}
          onPress={a.onPress}
        />
      ))}
    </>
  );
}

/**
 * Long-press → the system context menu, iMessage-style: the touched view
 * lifts out (clipped to `cornerRadius`) with the menu beside it. On iOS this
 * is a UIKit `UIContextMenuInteraction` attached directly to the React Native
 * view — never a SwiftUI host per row/bubble (see modules/native-menu).
 * Android falls back to the ActionSheet.
 */
export function LongPressMenu({
  actions,
  cornerRadius = 18,
  fill,
  title,
  disabled,
  previewBackground,
  style,
  children,
}: {
  actions: MenuAction[];
  cornerRadius?: number;
  /** Stretch to the parent's width (list rows) instead of hugging content. */
  fill?: boolean;
  /** Fallback sheet title (the native menu shows the lifted view instead). */
  title?: string;
  disabled?: boolean;
  /**
   * Fill behind the lifted preview. Needed when the child has no opaque
   * background of its own, or the preview's shadow shows through it.
   */
  previewBackground?: ColorValue;
  style?: StyleProp<ViewStyle>;
  children: React.ReactElement;
}) {
  if (disabled) return <View style={style}>{children}</View>;
  if (!nativeContextMenus) {
    return (
      <Pressable
        style={style}
        onLongPress={() =>
          showActionSheet({
            title,
            options: actions.map((a) => ({
              label: a.label,
              destructive: a.destructive,
              onPress: a.onPress,
            })),
          })
        }
        delayLongPress={500}
      >
        {children}
      </Pressable>
    );
  }
  return (
    <NativeContextMenu
      actions={toItems(actions)}
      cornerRadius={cornerRadius}
      previewBackgroundColor={previewBackground}
      onPressAction={(e) => pressById(actions, e.nativeEvent.id)}
      style={[fill && { alignSelf: "stretch" }, style]}
    >
      {children}
    </NativeContextMenu>
  );
}

/**
 * Round glass button that opens an anchored popover menu on tap (the iOS 26
 * "+" in Messages). A UIKit glass UIButton with its menu as the primary
 * action on iOS 26, so the menu morphs out of the button; GlassIconButton +
 * ActionSheet elsewhere.
 */
export function GlassMenuButton({
  actions,
  systemImage,
  fallbackIcon,
  label,
  title,
  size = 44,
}: {
  actions: MenuAction[];
  systemImage: SFSymbol;
  fallbackIcon: IconName;
  label: string;
  title?: string;
  size?: number;
}) {
  if (!nativeMenus) {
    return (
      <GlassIconButton
        icon={fallbackIcon}
        label={label}
        size={size}
        iconSize={24}
        onPress={() =>
          showActionSheet({
            title,
            options: actions.map((a) => ({
              label: a.label,
              destructive: a.destructive,
              onPress: a.onPress,
            })),
          })
        }
      />
    );
  }
  // Sized by React Native like the UIKit glass pill beside it — same
  // material, same layout, so the two can't drift apart the way a
  // self-sizing SwiftUI host could.
  return (
    <NativeMenuButton
      actions={toItems(actions)}
      systemImage={systemImage}
      iconSize={18}
      iconColor={colors.ink2}
      label={label}
      onPressAction={(e) => pressById(actions, e.nativeEvent.id)}
      style={{ width: size, height: size }}
    />
  );
}

/**
 * Tap → anchored popover menu, with any React Native view as the trigger.
 * Used where iOS shows a pull-down from an inline control (e.g. Edit Photo).
 */
export function TapMenu({
  actions,
  title,
  children,
}: {
  actions: MenuAction[];
  title?: string;
  children: React.ReactElement;
}) {
  if (!nativeMenus) {
    return (
      <Pressable
        onPress={() =>
          showActionSheet({
            title,
            options: actions.map((a) => ({
              label: a.label,
              destructive: a.destructive,
              onPress: a.onPress,
            })),
          })
        }
      >
        {children}
      </Pressable>
    );
  }
  return (
    <Host matchContents>
      <Menu
        label={<RNHostView matchContents>{children}</RNHostView>}
        modifiers={[menuIndicator("hidden"), buttonStyle("plain")]}
      >
        <MenuItems actions={actions} />
      </Menu>
    </Host>
  );
}
