import React, { useCallback, useEffect, useState } from "react";
import {
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import Animated, { useAnimatedStyle, useSharedValue } from "react-native-reanimated";
import { nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { dismissForChannel } from "../notify";
import { colors, type } from "../theme";
import { Composer, VOICE_LIFT } from "./Composer";
import { ImageViewer } from "./ImageViewer";
import { MessageList, Topbar, TOPBAR_H } from "./MessageList";
import { EdgeFade, GlassSurface } from "./Glass";

/**
 * The conversation surface. The message stream runs edge to edge; the glass
 * header and composer float over it, with scroll-edge fades so content
 * dissolves beneath them (the iOS 26 bar treatment). Shared between the
 * wide two-pane layout and the narrow pushed route — `fullBleed` says
 * whether this view owns the device safe areas.
 */
export function ConversationView({
  channel,
  showBack,
  fullBleed,
  onBack,
  onOpenProfile,
}: {
  channel: string;
  showBack: boolean;
  fullBleed: boolean;
  onBack?: () => void;
  onOpenProfile: (uid: number) => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const insets = useSafeAreaInsets();
  const [viewerUrl, setViewerUrl] = useState<string | null>(null);
  // Dock height includes the composer's reserved voice headroom, which is
  // empty at rest — the list's inset leaves it out.
  const [dockH, setDockH] = useState(56 + VOICE_LIFT);
  const composerH = dockH - VOICE_LIFT;
  // The voice morph grows the pill into that headroom; the list rides up with
  // it on the UI thread, frame-locked to the pill.
  const voiceTall = useSharedValue(0);
  const liftStyle = useAnimatedStyle(() => ({
    transform: [{ translateY: -VOICE_LIFT * voiceTall.value }],
  }));
  const keyboardUp = useKeyboardVisible();

  // Mark the channel active for read receipts; clear its notification.
  // In the narrow layout, leaving the screen deselects the channel so new
  // messages stop auto-marking read (the list becomes the "not viewing" state).
  useEffect(() => {
    if (store.state.activeChannel !== channel) store.switchChannel(channel);
    void dismissForChannel(channel);
    return () => {
      if (showBack && store.state.activeChannel === channel) store.switchChannel(null);
    };
  }, [channel, store, showBack]);

  const peerName = nameFor(s.users, store.peerOf(channel));
  const onImagePress = useCallback((url: string) => setViewerUrl(url), []);

  const top = fullBleed ? insets.top : 0;
  // The home-indicator inset only applies while the keyboard is down.
  const bottom = fullBleed && !keyboardUp ? insets.bottom : 0;
  const headerH = top + TOPBAR_H;

  return (
    <View style={styles.root}>
      <KeyboardAvoidingView
        style={styles.flex}
        behavior={Platform.OS === "ios" ? "padding" : "height"}
      >
        <View style={styles.flex}>
          <Animated.View style={[styles.flex, liftStyle]}>
            <MessageList
              channel={channel}
              onImagePress={onImagePress}
              topInset={headerH}
              bottomInset={composerH}
            />
          </Animated.View>
          <EdgeFade edge="bottom" height={composerH + 14} solid={bottom} />
          <View
            style={[styles.composerDock, { paddingBottom: Math.max(bottom, 6) }]}
            onLayout={(e) => setDockH(e.nativeEvent.layout.height)}
            pointerEvents="box-none"
          >
            <Composer peerName={peerName} voiceTall={voiceTall} />
          </View>
        </View>
      </KeyboardAvoidingView>

      <EdgeFade edge="top" height={headerH + 18} solid={top} />
      <View style={[styles.header, { paddingTop: top }]} pointerEvents="box-none">
        <Topbar
          channel={channel}
          showBack={showBack}
          onBack={onBack}
          onOpenProfile={onOpenProfile}
        />
        {s.connState === "reconnecting" && (
          <View style={styles.bannerWrap} pointerEvents="none">
            <GlassSurface style={styles.banner}>
              <View style={styles.bannerDot} />
              <Text style={styles.bannerText}>Reconnecting…</Text>
            </GlassSurface>
          </View>
        )}
      </View>

      <ImageViewer url={viewerUrl} onClose={() => setViewerUrl(null)} />
    </View>
  );
}

function useKeyboardVisible(): boolean {
  const [up, setUp] = useState(false);
  useEffect(() => {
    const showEvt = Platform.OS === "ios" ? "keyboardWillShow" : "keyboardDidShow";
    const hideEvt = Platform.OS === "ios" ? "keyboardWillHide" : "keyboardDidHide";
    const a = Keyboard.addListener(showEvt, () => setUp(true));
    const b = Keyboard.addListener(hideEvt, () => setUp(false));
    return () => {
      a.remove();
      b.remove();
    };
  }, []);
  return up;
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  flex: {
    flex: 1,
  },
  composerDock: {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 0,
  },
  header: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
  },
  bannerWrap: {
    alignItems: "center",
    marginTop: 2,
  },
  banner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 7,
    height: 30,
    paddingHorizontal: 12,
    borderRadius: 15,
  },
  bannerDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: colors.warn,
  },
  bannerText: {
    ...type.footnote,
    fontWeight: "600",
    color: colors.ink2,
  },
});
