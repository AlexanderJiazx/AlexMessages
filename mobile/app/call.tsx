import React, { useCallback, useEffect, useRef } from "react";
import { StyleSheet, View } from "react-native";
import { useIsFocused, useNavigation, useRouter } from "expo-router";
import { CallScreen, CALL_BG } from "../src/call/CallScreen";
import { useAppVisible, useCallView } from "../src/call/CallHost";
import { lockedOverCall, unlockToLeave } from "../src/call/system";

/**
 * The call screen, a full-screen modal over the app. CallHost opens it when
 * a call starts; it closes itself (back to wherever the call started) when
 * there is nothing left to show, and the chevron closes it mid-call.
 * CallScreen keeps its controls inside the safe areas itself, so the
 * peer's video can run under the status bar. On Android a call can show
 * over the lock screen: leaving this screen mid-call (the chevron, Back)
 * asks to unlock first, so the rest of the app stays locked.
 */
export default function CallRoute() {
  const { call, speaker, toggleSpeaker } = useCallView();
  const router = useRouter();
  const focused = useIsFocused();
  const visible = useAppVisible();

  const close = useCallback(() => {
    if (router.canGoBack()) router.back();
    else router.replace("/chat");
  }, [router]);

  // A call that ends in the background closes this screen on return.
  useEffect(() => {
    if (!call && focused && visible) close();
  }, [call, focused, visible, close]);

  const live = !!call && call.phase !== "ended";
  const liveRef = useRef(live);
  liveRef.current = live;
  const navigation = useNavigation();
  useEffect(
    () =>
      navigation.addListener("beforeRemove", (e) => {
        if (!liveRef.current || !lockedOverCall()) return;
        e.preventDefault();
        void unlockToLeave().then((unlocked) => {
          if (unlocked) navigation.dispatch(e.data.action);
        });
      }),
    [navigation]
  );

  return call ? (
    <CallScreen call={call} speaker={speaker} onToggleSpeaker={toggleSpeaker} onMinimize={close} />
  ) : (
    <View style={styles.root} />
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: CALL_BG,
  },
});
