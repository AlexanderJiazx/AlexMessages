// Android/web stub for @expo/ui/swift-ui: SwiftUI exists only on iOS, and
// every call site is gated behind Platform.OS === "ios" && glassSupported,
// so these are never rendered here — they exist only so the module resolves.
// (@expo/ui itself stays linked on Android: expo-router's Android Stack
// toolbar loads @expo/ui/jetpack-compose when expo-router is imported.)
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
