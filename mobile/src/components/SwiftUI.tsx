// Android/web stub for @expo/ui/swift-ui. @expo/ui is excluded from the
// Android build (it drags in the whole Jetpack Compose stack) and every call
// site is gated behind Platform.OS === "ios" && glassSupported, so these are
// never rendered here — they exist only so the module resolves.
import React from "react";

const Stub: React.ComponentType<Record<string, unknown>> = () => null;

export const Button = Stub;
export const Group = Stub;
export const Host = Stub;
export const Image = Stub;
export const Menu = Stub;
export const RNHostView = Stub;
export const ContextMenu: React.ComponentType<Record<string, unknown>> & {
  Items: React.ComponentType<Record<string, unknown>>;
  Trigger: React.ComponentType<Record<string, unknown>>;
} = Object.assign(Stub, { Items: Stub, Trigger: Stub });

const modifier = (..._args: unknown[]): undefined => undefined;

export const accessibilityLabel = modifier;
export const buttonStyle = modifier;
export const contentShape = modifier;
export const frame = modifier;
export const glassEffect = modifier;
export const menuIndicator = modifier;
export const shapes = { roundedRectangle: modifier };
