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
  Easing,
  useAnimatedScrollHandler,
  useAnimatedStyle,
  useSharedValue,
  withTiming,
  type SharedValue,
} from "react-native-reanimated";
import {
  KeyboardGestureArea,
  useGenericKeyboardHandler,
  useKeyboardState,
} from "react-native-keyboard-controller";
import { nameFor } from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../session";
import { dismissForChannel } from "../notify";
import { colors, glassSupported, type } from "../theme";
import { Composer, COMPOSER_INPUT_ID, COMPOSER_PAD, VOICE_LIFT } from "./Composer";
import { ImageViewer } from "./ImageViewer";
import type { ImagePressHandler } from "./attachments";
import { EdgeBlur, previewImage } from "../../modules/native-menu";
import { afterKeyboardCloses } from "../keyboard";
import { MessageList, Topbar, TOPBAR_H } from "./MessageList";
import { EdgeFade, GlassSurface } from "./Glass";
import { NoScrollEdgeEffects } from "./ScrollEdge";

/**
 * iOS follows the keyboard with react-native-keyboard-controller (see
 * app/_layout.tsx and useKeyboardLift): the composer and the message list
 * ride on the keyboard through the system animation and with the finger
 * during interactive dismissal. Android keeps the layout-driven
 * KeyboardAvoidingView.
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
  const { store, api } = useSession();
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
  // iOS: the native viewer (modules/native-menu), flying out of the bubble's
  // image; elsewhere the app's lightbox.
  const onImagePress = useCallback<ImagePressHandler>(
    (url, from) => {
      if (!ios) {
        setViewerUrl(url);
        return;
      }
      const full = url.startsWith("http") ? url : api.url(url);
      // The keyboard slides away first (presenting in its place drops it
      // without its animation and jumps the thread down).
      afterKeyboardCloses(() =>
        previewImage(full, from?.tag ?? null, from?.name).catch(() =>
          toast("Couldn't open the image", true)
        )
      );
    },
    [api]
  );

  const top = fullBleed ? insets.top : 0;
  // The home-indicator inset sits under the composer while the keyboard is
  // down. Android's KeyboardAvoidingView drops it while the keyboard is up;
  // on iOS it stays put and the keyboard lift absorbs it.
  const bottom = fullBleed && !keyboardUp ? insets.bottom : 0;
  // As in Messages, the composer's glass keeps one margin from the bottom and
  // from both sides: 6pt into the home-indicator inset (28pt on a 34pt
  // inset), never under 12pt (no home indicator, keyboard up on Android, the
  // wide layout's pane).
  const edgeInset = Math.max(bottom - 6, 12);
  const dockPad = edgeInset - COMPOSER_PAD;
  // iOS: how much of the keyboard's height the composer (and the list) must
  // rise by — the keyboard height less the dock's resting gap to the screen
  // bottom, keeping 6pt between the dock and the keyboard.
  const keyboardOffset = dockPad - 6 + (fullBleed ? 0 : insets.bottom);
  // iOS: a KeyboardGestureArea offset would extend the keyboard's grab area
  // above it by that many points via an invisible inputAccessoryView — but a
  // drag STARTING inside that zone snaps the keyboard's top edge under the
  // finger (a ~53pt jump in one frame). With offset 0 the zone is just the
  // keyboard, and composer drags dismiss through ComposerDragArea's scroll
  // overscroll instead — delta-tracked from the first frame, no snap. The
  // 0-height accessory still attaches, so the deferred-resign timing that
  // afterKeyboardCloses/Back rely on is unchanged.
  const gestureOffset = 0;
  const headerH = top + TOPBAR_H;
  // iOS: as in Messages, the dock also WIDENS while the keyboard lifts it —
  // the side margins shrink from the resting inset to the gap the glass
  // keeps above the keyboard (edgeInset - keyboardOffset), so the composer
  // keeps one visual margin on every side. Driven by the keyboard handler's
  // final-height events, so it animates in step with the keyboard itself.
  const kbUpInset = edgeInset - keyboardOffset;
  const kbWide = useSharedValue(0);

  // iOS: the composer rides on the keyboard — at rest above the home
  // indicator, 6pt above the keyboard once it's up, and during a drag on the
  // keyboard's top until it's back at rest (lifted by the keyboard height
  // beyond `keyboardOffset`). The list rides up by the same amount, so the
  // newest message stays glued above the composer. The list keeps its size
  // (no relayout, no re-pinning mid-animation); the part lifted off the top
  // stays reachable through `topSlack`.
  const keyboard = useKeyboardLift(gestureOffset, kbWide);
  const stickStyle = useAnimatedStyle(
    () => ({
      transform: [{ translateY: -Math.max(0, keyboard.value - keyboardOffset) }],
    }),
    [keyboardOffset]
  );
  const liftStyle = useAnimatedStyle(
    () => ({
      transform: [
        {
          translateY:
            (ios ? -Math.max(0, keyboard.value - keyboardOffset) : 0) -
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
  const dock = (
    <View
      style={{ paddingBottom: dockPad }}
      onLayout={(e) => setDockH(e.nativeEvent.layout.height)}
      pointerEvents="box-none"
    >
      <Composer
        peerName={peerName}
        voiceTall={voiceTall}
        sideInset={edgeInset}
        kbUpInset={kbUpInset}
        kbWide={kbWide}
      />
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
          <Animated.View style={[StyleSheet.absoluteFill, stickStyle]} pointerEvents="box-none">
            <ComposerDragArea>
              {dock}
            </ComposerDragArea>
          </Animated.View>
        </KeyboardGestureArea>
      ) : (
        <KeyboardAvoidingView style={styles.flex} behavior="height">
          <View style={styles.flex}>
            {stream}
            <View style={styles.composerDock} pointerEvents="box-none">
              {dock}
            </View>
          </View>
        </KeyboardAvoidingView>
      )}

      {/* Nothing is laid over the messages: the header capsule and the
          composer refract them directly. iOS 26 blurs them under the status
          bar and header (no tint); without liquid glass, a fade keeps text
          from running under the status bar. */}
      {glassSupported ? (
        <EdgeBlur edge="top" maxRadius={12} style={[styles.topBlur, { height: headerH + 18 }]} />
      ) : (
        <EdgeFade edge="top" height={headerH + 18} solid={top} />
      )}
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
    <NoScrollEdgeEffects>
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
    </NoScrollEdgeEffects>
  );
}

/**
 * iOS: the keyboard's height as the composer and the list follow it.
 *
 * Showing and hiding set the final height once, from the keyboard's
 * will-show/hide event — which runs inside the keyboard's own animation
 * block — so Core Animation moves the composer and the list along the
 * keyboard's exact curve on the render server: in step with the keyboard
 * and unaffected by work on the main or JS thread (the standard UIKit way
 * to animate alongside the keyboard). That includes the rest of a hide after
 * a drag is released: the keyboard's per-frame onMove positions are no use
 * there on iOS 26 — the first one is short by the accessory height (the
 * composer dropped behind the keyboard) and the rest follow a model curve,
 * not the keyboard.
 *
 * A drag moves the keyboard under the finger, frame by frame (onInteractive).
 */
function useKeyboardLift(gestureOffset: number, wide: SharedValue<number>): SharedValue<number> {
  const height = useSharedValue(0);
  const accessory = useSharedValue(gestureOffset);
  useEffect(() => {
    accessory.value = gestureOffset;
  }, [accessory, gestureOffset]);
  useGenericKeyboardHandler(
    {
      onStart: (e) => {
        "worklet";
        height.value = e.height;
        // e.height is the animation's TARGET height: >0 → ending up (dock
        // widens), 0 → dismissing (dock narrows back). Timing runs in step
        // with the keyboard's own animation.
        wide.value = withTiming(e.height > gestureOffset ? 1 : 0, {
          duration: 280,
          easing: Easing.inOut(Easing.quad),
        });
      },
      onInteractive: (e) => {
        "worklet";
        // Releasing a drag starts the dismissal inside UIKit's animation
        // block, and at that instant the keyboard frame is just the
        // KeyboardGestureArea's invisible accessory — reported as a drag
        // position equal to its height (the library skips the equivalent 0
        // without an accessory). Applied, that lifted the composer and the
        // list inside the animation block, which UIKit turned into an
        // additive animation: a 34pt drop below rest, easing back.
        if (Math.abs(e.height - accessory.value) < 0.5) return;
        height.value = e.height;
      },
      onEnd: (e) => {
        "worklet";
        height.value = e.height;
        wide.value = withTiming(e.height > gestureOffset ? 1 : 0, {
          duration: 280,
          easing: Easing.inOut(Easing.quad),
        });
      },
    },
    []
  );
  return height;
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
  topBlur: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
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
