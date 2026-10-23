import type { StyleProp, ViewStyle } from "react-native";
import type { CallStream } from "@alexmessages/shared";

/** Expo web has no calls (see platform.ts); iOS and Android load CallVideo.native.tsx. */
export function CallVideo(_props: {
  stream: CallStream | null;
  mirror?: boolean;
  zOrder?: number;
  style?: StyleProp<ViewStyle>;
  onFrames?: (flowing: boolean) => void;
}) {
  return null;
}
