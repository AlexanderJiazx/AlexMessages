import React from "react";
import type { StyleProp, ViewStyle } from "react-native";
import { RTCView, type MediaStream } from "react-native-webrtc";
import type { CallStream } from "@alexmessages/shared";

/**
 * One call video: react-native-webrtc's RTCView over a session stream.
 * `onFrames` reports whether frames are arriving (RTCView's size event:
 * a track exists from the offer on, but shows nothing until media flows).
 */
export function CallVideo({
  stream,
  mirror,
  zOrder,
  style,
  onFrames,
}: {
  stream: CallStream | null;
  mirror?: boolean;
  /** 0 for the full-screen video, 1 for a view drawn over it. */
  zOrder?: number;
  style?: StyleProp<ViewStyle>;
  onFrames?: (flowing: boolean) => void;
}) {
  if (!stream) return null;
  return (
    <RTCView
      streamURL={(stream as unknown as MediaStream).toURL()}
      objectFit="cover"
      mirror={mirror}
      zOrder={zOrder}
      style={style}
      onDimensionsChange={onFrames ? (e) => onFrames(e.nativeEvent.width > 0) : undefined}
    />
  );
}
