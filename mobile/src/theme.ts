/**
 * Design tokens — the "paper + sage" palette shared with the web client,
 * tuned for native surfaces. iOS 26+ layers real liquid glass on top
 * (see components/Glass.tsx); Android keeps flat Material surfaces.
 */
import { Platform } from "react-native";
import { isGlassEffectAPIAvailable, isLiquidGlassAvailable } from "expo-glass-effect";

export const colors = {
  bg: "#F6F4EE",
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
  danger: "#C4483F",
  overlay: "rgba(27,36,31,0.32)",
  /** Incoming message bubble — a warm grey that sits on `bg`. */
  bubbleThem: "#E9E8E0",
  /** iOS grouped-table background + separator. */
  grouped: "#F1EFE8",
  separator: "rgba(27,36,31,0.12)",
  /** Presence: the "online" dot and label, everywhere a user's status shows. */
  online: "#4CB86E",
  /** A plain list row while pressed. */
  rowPressed: "#EAE8E1",
  /** The swipe "Pin" action (the amber Messages uses). */
  pinAction: "#D99A2B",
  /** Settings row icon tiles. */
  iconCoral: "#D6694A",
  iconSlate: "#5B7FA6",
  /** Ink washes: quiet fills for chips and round buttons on paper. */
  fillFaint: "rgba(27,36,31,0.05)",
  fill: "rgba(27,36,31,0.06)",
  fillStrong: "rgba(27,36,31,0.08)",
  /** Ink scrims: a play button over a video, the ✕ badge on a thumbnail. */
  scrim: "rgba(27,36,31,0.55)",
  scrimStrong: "rgba(27,36,31,0.75)",
  /** Search fields and the login server chip. */
  searchFill: "rgba(118,128,112,0.13)",
  /** Danger washes: the recorder's stop button, a failed call card. */
  dangerWash: "rgba(196,72,63,0.12)",
  dangerWashPressed: "rgba(196,72,63,0.24)",
  dangerTint: "#FBECEA",
  /** Android image viewer: backdrop and its round close button. */
  viewerBackdrop: "rgba(10,12,11,0.92)",
  viewerButton: "rgba(255,255,255,0.14)",
};

export const radius = {
  s: 10,
  m: 14,
  l: 20,
  xl: 26,
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
    shadowOpacity: 0.14,
    shadowRadius: 22,
    shadowOffset: { width: 0, height: 8 },
    elevation: 6,
  },
};

/** Type scale — iOS Dynamic Type defaults (SF Pro) at "Large". */
export const type = {
  largeTitle: { fontSize: 34, fontWeight: "700" as const, letterSpacing: 0.37 },
  title2: { fontSize: 22, fontWeight: "700" as const, letterSpacing: 0.35 },
  headline: { fontSize: 17, fontWeight: "600" as const, letterSpacing: -0.43 },
  body: { fontSize: 17, fontWeight: "400" as const, letterSpacing: -0.43 },
  callout: { fontSize: 16, fontWeight: "400" as const, letterSpacing: -0.31 },
  subhead: { fontSize: 15, fontWeight: "400" as const, letterSpacing: -0.23 },
  footnote: { fontSize: 13, fontWeight: "400" as const, letterSpacing: -0.08 },
  caption: { fontSize: 12, fontWeight: "400" as const, letterSpacing: 0 },
};

/** Serif display face — matches the web's Instrument Serif brand mark. */
export const fontDisplay = Platform.select({ ios: "New York", default: "serif" }) || "serif";

/**
 * True on iOS 26+ where GlassView renders real liquid glass. Also guards the
 * early iOS 26 betas that shipped without the API (expo/expo#40911).
 */
export const glassSupported =
  Platform.OS === "ios" &&
  parseInt(String(Platform.Version), 10) >= 26 &&
  safe(isLiquidGlassAvailable) &&
  safe(isGlassEffectAPIAvailable);

function safe(fn: () => boolean): boolean {
  try {
    return fn();
  } catch {
    return false;
  }
}

/** Wide layout breakpoint — rail + chat side by side above this width. */
export const WIDE_BREAKPOINT = 720;

/** Press delay (ms) for highlightable rows inside scroll views. UIKit holds a
 *  touch in scrolling content for 150 ms before highlighting the cell
 *  (`delaysContentTouches`), so a scroll or a swipe never flashes it; a
 *  quick tap still highlights on release. Pressable's equivalent is
 *  `unstable_pressDelay`. */
export const ROW_PRESS_DELAY = 130;
