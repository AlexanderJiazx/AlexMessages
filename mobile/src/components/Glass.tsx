import React from "react";
import {
  Platform,
  Pressable,
  StyleSheet,
  View,
  type StyleProp,
  type ViewProps,
  type ViewStyle,
} from "react-native";
import { GlassView } from "expo-glass-effect";
import { colors, glassSupported, shadow } from "../theme";
import { Icon, type IconName } from "./Icon";

/**
 * The app's floating-material primitive. iOS 26+: real liquid glass
 * (`GlassView`). Older iOS: a translucent paper chip with a hairline edge.
 * Android: a flat Material surface with elevation. Same geometry everywhere,
 * so layouts don't shift between platforms.
 */
export function GlassSurface({
  style,
  interactive,
  tint,
  children,
  ...rest
}: ViewProps & {
  style?: StyleProp<ViewStyle>;
  interactive?: boolean;
  /** Optional tint (e.g. sage for a primary glass control). */
  tint?: string;
}) {
  if (glassSupported) {
    return (
      <GlassView
        style={style}
        glassEffectStyle="regular"
        isInteractive={interactive}
        tintColor={tint}
        colorScheme="light"
        {...rest}
      >
        {children}
      </GlassView>
    );
  }
  return (
    <View
      style={[
        Platform.OS === "android" ? styles.flatAndroid : styles.flatIOS,
        tint ? { backgroundColor: tint } : null,
        style,
      ]}
      {...rest}
    >
      {children}
    </View>
  );
}

/** Circular glass button — 44pt hit target, centered icon. */
export function GlassIconButton({
  icon,
  onPress,
  label,
  size = 44,
  iconSize = 20,
  color = colors.ink,
  tint,
  style,
  testID,
}: {
  icon: IconName;
  onPress: () => void;
  label: string;
  size?: number;
  iconSize?: number;
  color?: string;
  tint?: string;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}) {
  return (
    <Pressable
      onPress={onPress}
      accessibilityLabel={label}
      accessibilityRole="button"
      hitSlop={6}
      testID={testID}
      style={({ pressed }) => [pressed && !glassSupported && { opacity: 0.6 }, style]}
    >
      <GlassSurface
        interactive
        tint={tint}
        style={{
          width: size,
          height: size,
          borderRadius: size / 2,
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        <Icon name={icon} size={iconSize} color={color} />
      </GlassSurface>
    </Pressable>
  );
}

/**
 * Scroll-edge fade — the iOS 26 "content dissolves under the bar" effect,
 * done with a native linear gradient from the screen background to clear.
 */
export function EdgeFade({
  edge,
  height,
  solid = 0,
  color = colors.bg,
}: {
  edge: "top" | "bottom";
  height: number;
  /** Fully opaque band at the edge (e.g. behind the status bar). */
  solid?: number;
  color?: string;
}) {
  const dir = edge === "top" ? "to bottom" : "to top";
  const solidPct = Math.min(100, Math.round((solid / Math.max(height, 1)) * 100));
  const mid = solidPct + Math.round((100 - solidPct) * 0.45);
  return (
    <View
      pointerEvents="none"
      style={[
        styles.fade,
        edge === "top" ? { top: 0 } : { bottom: 0 },
        {
          height,
          experimental_backgroundImage: `linear-gradient(${dir}, ${color} 0%, ${color} ${solidPct}%, ${hexA(color, 0.85)} ${mid}%, ${hexA(color, 0)} 100%)`,
        },
      ]}
    />
  );
}

/** "#RRGGBB" + alpha → "rgba(r,g,b,a)". */
function hexA(hex: string, a: number): string {
  const h = hex.replace("#", "");
  const n = parseInt(h.length === 3 ? h.replace(/(.)/g, "$1$1") : h, 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
}

const styles = StyleSheet.create({
  flatIOS: {
    backgroundColor: "rgba(255,255,255,0.94)",
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.separator,
    ...shadow.card,
  },
  flatAndroid: {
    backgroundColor: colors.surface,
    elevation: 3,
  },
  fade: {
    position: "absolute",
    left: 0,
    right: 0,
  },
});
