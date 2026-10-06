import React from "react";
import { Ionicons } from "@expo/vector-icons";
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
