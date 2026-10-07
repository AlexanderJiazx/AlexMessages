import React from "react";
// Deep import: the package index eagerly requires every icon family's font
// file, which would bundle all ~20 vector-icon fonts (~4 MB). Only Ionicons
// is used, so importing it directly keeps the other fonts out of the app.
import Ionicons from "@expo/vector-icons/build/Ionicons";
import { colors } from "../theme";

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
