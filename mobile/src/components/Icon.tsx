import React from "react";
// Deep import: the package index eagerly requires every icon family's font
// file, which would bundle all ~20 vector-icon fonts (~4 MB). Only Ionicons
// is used, so importing it directly keeps the other fonts out of the app.
import Ionicons from "@expo/vector-icons/build/Ionicons";
import { Image } from "expo-image";
import type { SFSymbol } from "sf-symbols-typescript";
import { colors, glassSupported } from "../theme";

/**
 * Single icon surface — Ionicons everywhere so glyphs stay consistent.
 * `name` values mirror the Lucide set used on the web where possible.
 */
export type IconName = keyof typeof Ionicons.glyphMap;

export function Icon({
  name,
  size = 20,
  color = colors.ink2,
}: {
  name: IconName;
  size?: number;
  color?: string;
}) {
  return <Ionicons name={name} size={size} color={color} />;
}

/**
 * Loads the Ionicons font. An icon drawn before its font is loaded renders
 * empty until the load ends (~100 ms on an Android cold start), so the boot
 * gate (`app/index.tsx`) waits for this before it shows the first screen.
 * Never rejects: on a failure the icons load on their own, as before.
 */
export function loadIconFont(): Promise<void> {
  return Ionicons.loadFont().catch(() => {});
}

/**
 * An SF Symbol on iOS 26 (matching the app's other liquid-glass glyphs),
 * the Ionicons glyph elsewhere. Drawn by expo-image's `sf:` source — a
 * UIKit image view, not a SwiftUI host — so it is safe in list rows (see
 * mobile/AGENTS.md: SwiftUI hosts re-render on every scroll frame).
 */
export function SymbolIcon({
  sf,
  ion,
  size,
  color = colors.ink2,
}: {
  sf: SFSymbol;
  ion: IconName;
  /** The SF Symbol's point size; the Ionicons glyph is drawn 3pt larger. */
  size: number;
  color?: string;
}) {
  if (glassSupported) {
    return (
      <Image
        source={`sf:${sf}`}
        tintColor={color}
        style={{ fontSize: size, color }}
        accessible={false}
      />
    );
  }
  return <Icon name={ion} size={size + 3} color={color} />;
}
