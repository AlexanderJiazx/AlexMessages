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
import Animated, {
  useAnimatedScrollHandler,
  useAnimatedStyle,
  useSharedValue,
} from "react-native-reanimated";
import {
  KeyboardGestureArea,
  KeyboardStickyView,
  useKeyboardState,
  useReanimatedKeyboardAnimation,
} from "react-native-keyboard-controller";
import { nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { dismissForChannel } from "../notify";
import { colors, type } from "../theme";
import { Composer, COMPOSER_INPUT_ID, VOICE_LIFT } from "./Composer";
import { ImageViewer } from "./ImageViewer";
import { MessageList, Topbar, TOPBAR_H } from "./MessageList";
import { EdgeFade, GlassSurface } from "./Glass";

/**
 * iOS follows the keyboard on the UI thread (react-native-keyboard-controller,
 * see app/_layout.tsx): the composer and the message list move with the
 * system keyboard animation frame by frame, and with the finger during
 * interactive dismissal. Android keeps the layout-driven KeyboardAvoidingView.
 */
const ios = Platform.OS === "ios";

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
  const keyboardUp = useKeyboardVisible(!ios);

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
  // The home-indicator inset sits under the composer while the keyboard is
  // down. Android's KeyboardAvoidingView drops it while the keyboard is up;
  // on iOS it stays put and the keyboard lift absorbs it.
  const bottom = fullBleed && !keyboardUp ? insets.bottom : 0;
  const dockPad = Math.max(bottom, 6);
  // iOS: how much of the keyboard's height the composer (and the list) must
  // rise by — the keyboard height less the dock's resting gap to the screen
  // bottom, keeping 6pt between the dock and the keyboard.
  const keyboardOffset = dockPad - 6 + (fullBleed ? 0 : insets.bottom);
  // iOS: interactive dismissal grabs the keyboard once a drag reaches the
  // composer's top edge (as in Messages), not the keyboard's: the 6pt gap
  // plus the visible composer, without its empty voice headroom.
  const gestureOffset = 6 + dockH - dockPad - VOICE_LIFT;
  const headerH = top + TOPBAR_H;

  // iOS: the list rides up with the keyboard exactly like the composer (the
  // same transform KeyboardStickyView applies), so the newest message stays
  // glued above it — through the system animation and an interactive drag.
  // The list keeps its size (no relayout, no re-pinning mid-animation); the
  // part lifted off the top stays reachable through `topSlack`.
  const keyboard = useReanimatedKeyboardAnimation();
  const liftStyle = useAnimatedStyle(
    () => ({
      transform: [
        {
          translateY:
            (ios ? keyboard.height.value + keyboard.progress.value * keyboardOffset : 0) -
            VOICE_LIFT * voiceTall.value,
        },
      ],
    }),
    [keyboardOffset]
  );
  const keyboardHeight = useKeyboardState((k) => (k.isVisible ? k.height : 0));
  const topSlack = ios ? Math.max(0, keyboardHeight - keyboardOffset) : 0;

  const stream = (
    <Animated.View style={[styles.flex, liftStyle]}>
      <MessageList
        channel={channel}
        onImagePress={onImagePress}
        topInset={headerH}
        bottomInset={composerH}
        topSlack={topSlack}
      />
    </Animated.View>
  );
  const fade = <EdgeFade edge="bottom" height={composerH + 14} solid={bottom} />;
  const dock = (
    <View
      style={{ paddingBottom: dockPad }}
      onLayout={(e) => setDockH(e.nativeEvent.layout.height)}
      pointerEvents="box-none"
    >
      <Composer peerName={peerName} voiceTall={voiceTall} />
    </View>
  );

  return (
    <View style={styles.root}>
      {ios ? (
        <KeyboardGestureArea
          style={styles.flex}
          textInputNativeID={COMPOSER_INPUT_ID}
          offset={gestureOffset}
        >
          {stream}
          <KeyboardStickyView
            style={StyleSheet.absoluteFill}
            offset={{ closed: 0, opened: keyboardOffset }}
            pointerEvents="box-none"
          >
            <ComposerDragArea>
              {fade}
              {dock}
            </ComposerDragArea>
          </KeyboardStickyView>
        </KeyboardGestureArea>
      ) : (
        <KeyboardAvoidingView style={styles.flex} behavior="height">
          <View style={styles.flex}>
            {stream}
            {fade}
            <View style={styles.composerDock} pointerEvents="box-none">
              {dock}
            </View>
          </View>
        </KeyboardAvoidingView>
      )}

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

/**
 * iOS: drag the composer down to pull the keyboard away, as in Messages.
 * UIKit only hands the keyboard to a finger through a scroll view with
 * interactive dismissal that is actually being scrolled, so the composer sits
 * at the bottom of a transparent, full-height one. It never takes touches
 * itself (box-none), so drags on the message list still scroll the list.
 * The scroll view's own rubber-banding is cancelled out on the UI thread —
 * the composer only follows the keyboard; moving with the finger as well
 * would run it ahead of the keyboard and behind it. With the keyboard down
 * there is nothing to dismiss, and it stops scrolling.
 */
function ComposerDragArea({ children }: { children: React.ReactNode }) {
  const keyboardShown = useKeyboardState((k) => k.isVisible);
  const offset = useSharedValue(0);
  const onScroll = useAnimatedScrollHandler((e) => {
    offset.value = e.contentOffset.y;
  });
  const holdStyle = useAnimatedStyle(() => ({
    transform: [{ translateY: offset.value }],
  }));
  return (
    <Animated.ScrollView
      style={styles.dragArea}
      contentContainerStyle={styles.dragContent}
      pointerEvents="box-none"
      scrollEnabled={keyboardShown}
      alwaysBounceVertical
      keyboardDismissMode="interactive"
      keyboardShouldPersistTaps="always"
      contentInsetAdjustmentBehavior="never"
      showsVerticalScrollIndicator={false}
      scrollsToTop={false}
      onScroll={onScroll}
      scrollEventThrottle={16}
    >
      <Animated.View style={[styles.dragContent, holdStyle]} pointerEvents="box-none">
        {children}
      </Animated.View>
    </Animated.ScrollView>
  );
}

/** Android only: the KeyboardAvoidingView path drops the bottom inset while typing. */
function useKeyboardVisible(enabled: boolean): boolean {
  const [up, setUp] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    const a = Keyboard.addListener("keyboardDidShow", () => setUp(true));
    const b = Keyboard.addListener("keyboardDidHide", () => setUp(false));
    return () => {
      a.remove();
      b.remove();
    };
  }, [enabled]);
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
  dragArea: {
    flex: 1,
    overflow: "visible",
  },
  dragContent: {
    flexGrow: 1,
    justifyContent: "flex-end",
    pointerEvents: "box-none",
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
