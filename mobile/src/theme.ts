/**
 * Design tokens — the same "paper + sage" palette as the web client's
 * styles.css :root, expressed as RN constants. iOS layers liquid glass on
 * top; Android keeps flat surfaces. One theme, two materials.
 */
import { Platform } from "react-native";

export const colors = {
  bg: "#F1EFE6",
  paper: "#FAF8F1",
  surface: "#FFFFFF",
  ink: "#1B241F",
  ink2: "#344039",
  muted: "#6E7A72",
  faint: "#9AA59D",
  line: "#E2E4D9",
  line2: "#EAEBE1",
  sage: "#4F7A5E",
  sageDeep: "#355040",
  sageSoft: "#DCE9D9",
  sageMint: "#C7E1C5",
  sageTint: "#ECF3E8",
  sageGlow: "#B9DFB7",
  warn: "#C58A3B",
  danger: "#B85450",
  overlay: "rgba(27,36,31,0.32)",
};

export const radius = {
  s: 10,
  m: 14,
  l: 20,
  pill: 999,
};

export const shadow = {
  card: {
    shadowColor: "#1B241F",
    shadowOpacity: 0.08,
    shadowRadius: 16,
    shadowOffset: { width: 0, height: 6 },
    elevation: 3,
  },
  float: {
    shadowColor: "#1B241F",
    shadowOpacity: 0.18,
    shadowRadius: 28,
    shadowOffset: { width: 0, height: 12 },
    elevation: 8,
  },
};

/** Serif display face for the "Messages" title — matches Instrument Serif. */
export const fontDisplay = Platform.select({ ios: "New York", default: "serif" }) || "serif";

/** True on iOS 26+ where GlassView renders real liquid glass. */
export const glassSupported =
  Platform.OS === "ios" && parseInt(String(Platform.Version), 10) >= 26;

/** Wide layout breakpoint — rail + chat side by side above this width. */
export const WIDE_BREAKPOINT = 720;
