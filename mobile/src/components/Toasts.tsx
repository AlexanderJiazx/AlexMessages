import React, { useEffect, useRef, useState } from "react";
import { Animated, StyleSheet, Text, View } from "react-native";
import { onToast } from "../session";
import { colors, radius, shadow } from "../theme";

interface Toast {
  id: number;
  text: string;
  isErr: boolean;
}

/** Pill toasts bottom-center — same cadence as the web client's toast stack. */
export function Toasts() {
  const [items, setItems] = useState<Toast[]>([]);
  const nextId = useRef(1);

  useEffect(
    () =>
      onToast((t) => {
        const id = nextId.current++;
        setItems((xs) => [...xs, { id, text: t.text, isErr: t.isErr }]);
        setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), 3200);
      }),
    []
  );

  if (!items.length) return null;
  return (
    <View style={styles.wrap} pointerEvents="none">
      {items.map((t) => (
        <ToastPill key={t.id} toast={t} />
      ))}
    </View>
  );
}

function ToastPill({ toast }: { toast: Toast }) {
  const opacity = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    Animated.timing(opacity, { toValue: 1, duration: 160, useNativeDriver: true }).start();
  }, [opacity]);
  return (
    <Animated.View
      style={[styles.pill, toast.isErr && styles.pillErr, { opacity }]}
      accessibilityLiveRegion="polite"
    >
      <Text style={styles.text}>{toast.text}</Text>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    position: "absolute",
    bottom: 120,
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
