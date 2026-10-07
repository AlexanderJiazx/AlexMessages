// Android/web stub for @expo/ui/swift-ui. @expo/ui is excluded from the
// Android build (it drags in the whole Jetpack Compose stack) and every call
// site is gated behind Platform.OS === "ios" && glassSupported, so these are
// never rendered here — they exist only so the module resolves.
import React from "react";

const Stub: React.ComponentType<Record<string, unknown>> = () => null;

export const Button = Stub;
export const Host = Stub;
export const Image = Stub;
export const Menu = Stub;
export const RNHostView = Stub;

const modifier = (..._args: unknown[]): undefined => undefined;

export const buttonStyle = modifier;
export const menuIndicator = modifier;
