import React, { useCallback, useEffect } from "react";
import { Keyboard, Platform, StyleSheet, View } from "react-native";
import { Redirect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { KeyboardController } from "react-native-keyboard-controller";
import { isDM } from "@alexmessages/shared";
import { useSession } from "../src/session";
import { colors } from "../src/theme";
import { afterKeyboardCloses } from "../src/keyboard";
import { ConversationView } from "../src/components/ConversationView";
import { Toasts } from "../src/components/Toasts";

/** Pushed conversation screen — the narrow-layout chat surface. */
export default function ConversationScreen() {
  const { phase } = useSession();
  const router = useRouter();
  const { channel } = useLocalSearchParams<{ channel: string }>();

  const onOpenProfile = useCallback(
    (uid: number) => router.push({ pathname: "/profile", params: { uid: String(uid) } }),
    [router]
  );
  const onBack = useCallback(() => router.back(), [router]);

  // Leaving the thread takes the keyboard down with it, as in Messages. A
  // Back tap holds the pop until the keyboard has started to hide (see
  // afterKeyboardCloses — popping first left it up over the DM list for
  // good). The swipe-back gesture needs nothing: UIKit resigns the input as
  // the swipe begins, so the keyboard is already down when the route goes.
  const navigation = useNavigation();
  useEffect(() => {
    // "waiting": the pop is held (a second Back tap meanwhile is swallowed);
    // "released": the held pop, re-dispatched, goes through.
    let hold: "none" | "waiting" | "released" = "none";
    return navigation.addListener("beforeRemove", (e) => {
      if (hold === "released") return;
      if (hold === "none" && !(Platform.OS === "ios" && KeyboardController.isVisible())) {
        if (Platform.OS !== "ios") Keyboard.dismiss();
        return;
      }
      e.preventDefault();
      if (hold === "waiting") return;
      hold = "waiting";
      afterKeyboardCloses(() => {
        hold = "released";
        navigation.dispatch(e.data.action);
      });
    });
  }, [navigation]);

  if (phase === "login") return <Redirect href="/login" />;
  if (!channel || !isDM(channel)) return <Redirect href="/chat" />;

  return (
    <View style={styles.root}>
      <ConversationView
        channel={channel}
        showBack
        fullBleed
        onBack={onBack}
        onOpenProfile={onOpenProfile}
      />
      <Toasts />
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
});
