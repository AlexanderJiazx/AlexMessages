import React, { useEffect, useRef, useState } from "react";
import { Animated, Keyboard, Platform, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { FullWindowOverlay } from "react-native-screens";
import { usePathname } from "expo-router";
import { onToast } from "../session";
import { colors, radius, shadow } from "../theme";

interface Toast {
  id: number;
  text: string;
  isErr: boolean;
  leaving: boolean;
}

/** How long a toast stays fully shown, then how long it takes to fade. */
const SHOW_MS = 3000;
const FADE_IN_MS = 160;
const FADE_OUT_MS = 220;
/** Gap between the toasts and the keyboard or the home indicator: clears
 *  the conversation's composer, which sits just above either one. */
const CLEARANCE = 96;
/** On the call screen: clears the call controls. */
const CALL_CLEARANCE = 150;

/**
 * The app's one toast stack — pill toasts bottom-center, the web client's
 * cadence. Mounted once at the root (app/_layout.tsx), so a toast shows over
 * every screen: on iOS it renders in a react-native-screens
 * FullWindowOverlay (only while a toast is up), which sits above the modal
 * sheets (Settings, the contact card, New Message) that iOS presents above
 * the root view; on Android the sheets are views in the root view and the
 * host, drawn after the Stack, is above them. It sits above the keyboard
 * while the keyboard is up (the keyboard is a window of its own on iOS),
 * and higher on the call screen, above the call controls.
 */
export function ToastHost() {
  const [items, setItems] = useState<Toast[]>([]);
  const nextId = useRef(1);
  const insets = useSafeAreaInsets();
  const keyboard = useKeyboardHeight();
  const onCall = usePathname() === "/call";

  useEffect(() => {
    const timers = new Set<ReturnType<typeof setTimeout>>();
    const later = (fn: () => void, ms: number) => {
      const t = setTimeout(() => {
        timers.delete(t);
        fn();
      }, ms);
      timers.add(t);
    };
    const off = onToast((t) => {
      const id = nextId.current++;
      setItems((xs) => [...xs, { id, text: t.text, isErr: t.isErr, leaving: false }]);
      later(() => setItems((xs) => xs.map((x) => (x.id === id ? { ...x, leaving: true } : x))), SHOW_MS);
      later(() => setItems((xs) => xs.filter((x) => x.id !== id)), SHOW_MS + FADE_OUT_MS);
    });
    return () => {
      off();
      for (const t of timers) clearTimeout(t);
    };
  }, []);

  if (!items.length) return null;
  const bottom = (keyboard > 0 ? keyboard : insets.bottom) + (onCall ? CALL_CLEARANCE : CLEARANCE);
  const stack = (
    <View style={[styles.wrap, { bottom }]} pointerEvents="none">
      {items.map((t) => (
        <ToastPill key={t.id} toast={t} />
      ))}
    </View>
  );
  if (Platform.OS !== "ios") return stack;
  // Not modal for VoiceOver: the app under the toast stays reachable.
  return <FullWindowOverlay unstable_accessibilityContainerViewIsModal={false}>{stack}</FullWindowOverlay>;
}

function ToastPill({ toast }: { toast: Toast }) {
  const opacity = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    Animated.timing(opacity, {
      toValue: toast.leaving ? 0 : 1,
      duration: toast.leaving ? FADE_OUT_MS : FADE_IN_MS,
      useNativeDriver: true,
    }).start();
  }, [opacity, toast.leaving]);
  return (
    <Animated.View
      style={[styles.pill, toast.isErr && styles.pillErr, { opacity }]}
      accessibilityLiveRegion="polite"
    >
      <Text style={styles.text}>{toast.text}</Text>
    </Animated.View>
  );
}

/** The keyboard's height while it is up, else 0. iOS reports it as the
 *  keyboard starts to move; Android once it has moved. */
function useKeyboardHeight(): number {
  const [height, setHeight] = useState(0);
  useEffect(() => {
    const ios = Platform.OS === "ios";
    const show = Keyboard.addListener(ios ? "keyboardWillShow" : "keyboardDidShow", (e) =>
      setHeight(e.endCoordinates.height)
    );
    const hide = Keyboard.addListener(ios ? "keyboardWillHide" : "keyboardDidHide", () => setHeight(0));
    return () => {
      show.remove();
      hide.remove();
    };
  }, []);
  return height;
}

const styles = StyleSheet.create({
  wrap: {
    position: "absolute",
    left: 0,
    right: 0,
    alignItems: "center",
    gap: 8,
  },
  pill: {
    backgroundColor: colors.ink,
    borderRadius: radius.pill,
    paddingHorizontal: 18,
    paddingVertical: 10,
    maxWidth: "84%",
    ...shadow.float,
  },
  pillErr: {
    backgroundColor: colors.danger,
  },
  text: {
    color: colors.surface,
    fontSize: 14,
    fontWeight: "500",
  },
});
