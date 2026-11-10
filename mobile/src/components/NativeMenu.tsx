import React, { useMemo, useRef, useState } from "react";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import {
  Platform,
  Pressable,
  View,
  type ColorValue,
  type ImageSourcePropType,
  type StyleProp,
  type ViewStyle,
} from "react-native";
import { Button, Host, Menu, RNHostView, buttonStyle, menuIndicator } from "./SwiftUI";
import * as Compose from "./JetpackCompose";
import type { SFSymbol } from "sf-symbols-typescript";
import { NativeContextMenu, NativeMenuButton, type NativeMenuItem } from "../../modules/native-menu";
import { colors, glassSupported } from "../theme";
import { showActionSheet } from "./ActionSheet";
import { GlassIconButton } from "./Glass";
import type { IconName } from "./Icon";

/** One menu entry — rendered as a native UIMenu item on iOS. */
export interface MenuAction {
  /**
   * Identifies the entry when the native menu reports a tap. Defaults to
   * the label. Give an entry whose effect changes with state (Pin / Unpin)
   * a label or id that changes with it, so a menu left open across the
   * change can't run the other action.
   */
  id?: string;
  label: string;
  /** SF Symbol shown beside the label on iOS. */
  systemImage?: SFSymbol;
  /**
   * Leading icon in the Android dropdown (GlassMenuButton `dropdown`): an
   * Android vector drawable, `require("…/assets/icons/<name>.xml")`.
   */
  androidIcon?: ImageSourcePropType;
  destructive?: boolean;
  onPress: () => void;
}

/** UIKit context menus work on every supported iOS; Android uses ActionSheet. */
const nativeContextMenus = Platform.OS === "ios";
/** The glass menu button (and SwiftUI pull-downs) need iOS 26. */
const nativeMenus = Platform.OS === "ios" && glassSupported;

const actionId = (a: MenuAction) => a.id ?? a.label;

/**
 * Menu entries for the UIKit views. Each carries a stable id (not its
 * position): the menu can stay open while the list behind it re-renders
 * (an entry added or removed), and a tap must still run the entry the user
 * saw, or nothing if that entry is gone.
 */
function toItems(actions: MenuAction[]): NativeMenuItem[] {
  return actions.map((a) => ({
    id: actionId(a),
    title: a.label,
    systemImage: a.systemImage,
    destructive: a.destructive,
  }));
}

function pressById(actions: MenuAction[], id: string) {
  actions.find((a) => actionId(a) === id)?.onPress();
}

function MenuItems({ actions }: { actions: MenuAction[] }) {
  return (
    <>
      {actions.map((a) => (
        <Button
          key={actionId(a)}
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
      <SheetOnLongPress actions={actions} title={title} style={[fill && { alignSelf: "stretch" }, style]}>
        {children}
      </SheetOnLongPress>
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
 * Android / web: a long press opens the action sheet. It is a gesture-handler
 * long press, not a wrapping Pressable: the children are pressables
 * themselves (a DM row, a pinned tile, an image, a link), and the innermost
 * pressable owns the touch, so a wrapping Pressable's onLongPress never
 * fired and the hold ended as a tap on the child. The native recognizer
 * sees the hold whatever is inside, and once it fires it cancels the
 * child's touch, so no tap follows.
 */
function SheetOnLongPress({
  actions,
  title,
  style,
  children,
}: {
  actions: MenuAction[];
  title?: string;
  style?: StyleProp<ViewStyle>;
  children: React.ReactElement;
}) {
  const latest = useRef({ actions, title });
  latest.current = { actions, title };
  const gesture = useMemo(
    () =>
      Gesture.LongPress()
        .minDuration(500) // React Native's default delayLongPress
        .runOnJS(true)
        .onStart(() => {
          const { actions: list, title: heading } = latest.current;
          showActionSheet({
            title: heading,
            options: list.map((a) => ({
              label: a.label,
              destructive: a.destructive,
              onPress: a.onPress,
            })),
          });
        }),
    []
  );
  return (
    <GestureDetector gesture={gesture}>
      <View style={style} collapsable={false}>
        {children}
      </View>
    </GestureDetector>
  );
}

/**
 * Round glass button that opens an anchored popover menu on tap (the iOS 26
 * "+" in Messages). A UIKit glass UIButton with its menu as the primary
 * action on iOS 26, so the menu morphs out of the button; GlassIconButton +
 * ActionSheet elsewhere — or, with `dropdown`, a Material dropdown anchored
 * to the button on Android.
 */
export function GlassMenuButton({
  actions,
  systemImage,
  fallbackIcon,
  label,
  title,
  size = 44,
  iconSize = 18,
  fallbackIconSize = 24,
  iconColor = colors.ink2,
  dropdown,
}: {
  actions: MenuAction[];
  systemImage: SFSymbol;
  fallbackIcon: IconName;
  label: string;
  title?: string;
  size?: number;
  /** The SF Symbol's point size (iOS 26). */
  iconSize?: number;
  /** The Ionicons glyph's size (elsewhere). */
  fallbackIconSize?: number;
  /** The SF Symbol's color (iOS 26). */
  iconColor?: string;
  /**
   * Android: open a Material dropdown under the button (a top-bar menu)
   * instead of the bottom sheet (the composer "+" keeps the sheet).
   */
  dropdown?: boolean;
}) {
  if (dropdown && Platform.OS === "android") {
    return (
      <DropdownButton actions={actions} icon={fallbackIcon} label={label} size={size} iconSize={fallbackIconSize} />
    );
  }
  if (!nativeMenus) {
    return (
      <GlassIconButton
        icon={fallbackIcon}
        label={label}
        size={size}
        iconSize={fallbackIconSize}
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
      iconSize={iconSize}
      iconColor={iconColor}
      label={label}
      onPressAction={(e) => pressById(actions, e.nativeEvent.id)}
      style={{ width: size, height: size }}
    />
  );
}

/**
 * Android: the round button with a Material 3 dropdown anchored to it — the
 * Jetpack Compose `DropdownMenu` (via @expo/ui), with the React Native button
 * hosted as its trigger. Light, on the app's paper, whatever the device theme;
 * `seedColor` keeps any color not set here sage instead of the wallpaper's.
 */
function DropdownButton({
  actions,
  icon,
  label,
  size,
  iconSize,
}: {
  actions: MenuAction[];
  icon: IconName;
  label: string;
  size: number;
  iconSize: number;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Compose.Host matchContents colorScheme="light" seedColor={colors.sage}>
      <Compose.DropdownMenu expanded={open} onDismissRequest={() => setOpen(false)} color={colors.paper}>
        <Compose.DropdownMenu.Trigger>
          <Compose.RNHostView matchContents>
            <GlassIconButton icon={icon} label={label} size={size} iconSize={iconSize} onPress={() => setOpen(true)} />
          </Compose.RNHostView>
        </Compose.DropdownMenu.Trigger>
        <Compose.DropdownMenu.Items>
          {actions.map((a) => {
            const tint = a.destructive ? colors.danger : colors.ink;
            return (
              <Compose.DropdownMenuItem
                key={actionId(a)}
                onClick={() => {
                  setOpen(false);
                  a.onPress();
                }}
              >
                {a.androidIcon && (
                  <Compose.DropdownMenuItem.LeadingIcon>
                    <Compose.Icon source={a.androidIcon} size={22} tint={tint} />
                  </Compose.DropdownMenuItem.LeadingIcon>
                )}
                <Compose.DropdownMenuItem.Text>
                  <Compose.Text color={tint} style={{ typography: "bodyLarge" }}>
                    {a.label}
                  </Compose.Text>
                </Compose.DropdownMenuItem.Text>
              </Compose.DropdownMenuItem>
            );
          })}
        </Compose.DropdownMenu.Items>
      </Compose.DropdownMenu>
    </Compose.Host>
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
