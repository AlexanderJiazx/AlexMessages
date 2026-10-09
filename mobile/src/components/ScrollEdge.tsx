import React from "react";
import { Platform, StyleSheet, type ViewProps } from "react-native";
import { ScrollViewMarker } from "react-native-screens/experimental";

// The native marker is a plain React Native view, so it takes `pointerEvents`
// (passed straight through); its TS props just don't list it.
const Marker = ScrollViewMarker as React.ComponentType<
  React.ComponentProps<typeof ScrollViewMarker> & Pick<ViewProps, "pointerEvents">
>;

const HIDDEN = { top: "hidden", bottom: "hidden", left: "hidden", right: "hidden" } as const;

/**
 * Turns off iOS 26's automatic scroll-edge effect on the single ScrollView /
 * FlatList inside. Besides blurring, it fades content toward white (on the
 * conversation it whitened the header's clear glass), and on the inverted
 * message list (a flipped scroll view) its "top" edge is the bottom of the
 * screen, over the newest messages. The conversation draws its own top blur
 * (EdgeBlur) instead.
 *
 * The route-level `scrollEdgeEffects` option can't do this: react-native-screens
 * applies it to just one scroll view — the one on the screen's first-child
 * chain, looked up once when the screen mounts — so other lists keep it.
 */
export function NoScrollEdgeEffects({ children }: { children: React.ReactElement }) {
  if (Platform.OS !== "ios") return children;
  return (
    // box-none: the marker fills its parent and must never take a touch
    // itself — over the full-screen composer drag area it would otherwise
    // swallow every touch meant for the message list beneath.
    <Marker scrollEdgeEffects={HIDDEN} style={styles.fill} pointerEvents="box-none">
      {children}
    </Marker>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
});
