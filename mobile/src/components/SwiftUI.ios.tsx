// iOS: real SwiftUI primitives from @expo/ui. The Android variant
// (SwiftUI.tsx) is a stub — @expo/ui is excluded from the Android build,
// so this module must never resolve there.
export { Button, ContextMenu, Group, Host, Image, Menu, RNHostView } from "@expo/ui/swift-ui";
export {
  accessibilityLabel,
  buttonStyle,
  contentShape,
  frame,
  glassEffect,
  menuIndicator,
  shapes,
} from "@expo/ui/swift-ui/modifiers";
