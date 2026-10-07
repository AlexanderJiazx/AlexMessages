import React from "react";
import { Platform, Pressable, View, type StyleProp, type ViewStyle } from "react-native";
import { Button, ContextMenu, Group, Host, Image, Menu, RNHostView } from "@expo/ui/swift-ui";
import {
  accessibilityLabel,
  buttonStyle,
  contentShape,
  frame,
  glassEffect,
  menuIndicator,
  shapes,
} from "@expo/ui/swift-ui/modifiers";
import type { SFSymbol } from "sf-symbols-typescript";
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

/** Native menus need iOS 26's SwiftUI stack; elsewhere we use ActionSheet. */
const nativeMenus = Platform.OS === "ios" && glassSupported;

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
 * lifts out (clipped to `cornerRadius`) with the menu beside it. Android and
 * pre-26 iOS fall back to the ActionSheet.
 */
export function LongPressMenu({
  actions,
  cornerRadius = 18,
  fill,
  title,
  disabled,
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
  style?: StyleProp<ViewStyle>;
  children: React.ReactElement;
}) {
  if (disabled) return <View style={style}>{children}</View>;
  if (!nativeMenus) {
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
    <Host
      matchContents={fill ? { vertical: true, horizontal: false } : true}
      style={[fill && { alignSelf: "stretch" }, style]}
    >
      <ContextMenu>
        <ContextMenu.Items>
          <MenuItems actions={actions} />
        </ContextMenu.Items>
        <ContextMenu.Trigger>
          <Group
            modifiers={[
              contentShape(shapes.roundedRectangle({ cornerRadius }), "contextMenuPreview"),
            ]}
          >
            <RNHostView matchContents>{children}</RNHostView>
          </Group>
        </ContextMenu.Trigger>
      </ContextMenu>
    </Host>
  );
}

/**
 * Round glass button that opens an anchored popover menu on tap (the iOS 26
 * "+" in Messages). Native SwiftUI glass button + Menu on iOS 26, so the menu
 * morphs out of the button; GlassIconButton + ActionSheet elsewhere.
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
  return (
    <Host matchContents>
      <Menu
        label={
          // Sized and glassed explicitly (not a .glass button style, which pads
          // the label and comes out larger) so it matches the 44pt composer pill.
          <Image
            systemName={systemImage}
            size={18}
            color={colors.ink2}
            modifiers={[
              frame({ width: size, height: size }),
              glassEffect({ glass: { variant: "regular", interactive: true }, shape: "circle" }),
            ]}
          />
        }
        modifiers={[buttonStyle("plain"), menuIndicator("hidden"), accessibilityLabel(label)]}
      >
        <MenuItems actions={actions} />
      </Menu>
    </Host>
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
