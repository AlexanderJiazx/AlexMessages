import React, { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  interpolate,
  runOnJS,
  useAnimatedStyle,
  useSharedValue,
  withSpring,
  withTiming,
  type SharedValue,
} from "react-native-reanimated";
import * as Haptics from "expo-haptics";
import type { SFSymbol } from "sf-symbols-typescript";
import { colors, type } from "../theme";
import type { IconName } from "./Icon";
import { Glyph } from "./DictationBar";

/** One swipe button. The first action on a side is the outermost one and
 *  is what a full swipe triggers. */
export interface SwipeAction {
  key: string;
  label: string;
  sf: SFSymbol;
  ion: IconName;
  color: string;
  onPress: () => void;
}

const ACTION_W = 76;
/** A swipe past this share of the row width arms the full-swipe action. */
const FULL = 0.55;
const SPRING = { damping: 26, stiffness: 260, mass: 0.9 };

/** Only one row is open at a time (like Messages); the list closes it on scroll. */
let closeOpen: (() => void) | null = null;
export function closeOpenSwipeRow() {
  closeOpen?.();
}

/**
 * iMessage-style swipe actions for a list row: drag right to reveal
 * `leading`, left to reveal `trailing`. Buttons share the revealed space;
 * dragging past ~55% of the width arms the outermost action (it grows to fill
 * the gap, with a haptic tick) and releasing triggers it. Tapping the row
 * while open just closes it. Vertical drags fall through to the list.
 */
export function SwipeRow({
  width,
  leading = [],
  trailing = [],
  children,
}: {
  width: number;
  leading?: SwipeAction[];
  trailing?: SwipeAction[];
  children: React.ReactNode;
}) {
  const tx = useSharedValue(0);
  const start = useSharedValue(0);
  const armed = useSharedValue(0);
  const armedTarget = useSharedValue(0);
  const [open, setOpen] = useState(false);
  const leadW = leading.length * ACTION_W;
  const trailW = trailing.length * ACTION_W;

  const close = useCallback(() => {
    tx.value = withSpring(0, SPRING);
    armed.value = withTiming(0, { duration: 150 });
    armedTarget.value = 0;
    setOpen(false);
  }, [tx, armed, armedTarget]);

  const closeRef = useRef(close);
  closeRef.current = close;
  useEffect(
    () => () => {
      if (closeOpen === closeRef.current) closeOpen = null;
    },
    []
  );

  const onBegin = useCallback(() => {
    if (closeOpen && closeOpen !== closeRef.current) closeOpen();
    closeOpen = closeRef.current;
  }, []);

  const tick = useCallback(() => {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium).catch(() => {});
  }, []);

  const fire = useCallback(
    (side: "leading" | "trailing") => {
      const a = side === "leading" ? leading[0] : trailing[0];
      close();
      a?.onPress();
    },
    [leading, trailing, close]
  );

  const settle = useCallback((isOpen: boolean) => setOpen(isOpen), []);

  const pan = Gesture.Pan()
    .activeOffsetX([-12, 12])
    .failOffsetY([-10, 10])
    .onStart(() => {
      start.value = tx.value;
      runOnJS(onBegin)();
    })
    .onUpdate((e) => {
      let x = start.value + e.translationX;
      // No actions on a side: rubber-band instead of revealing nothing.
      if (x > 0 && !leadW) x = x * 0.15;
      if (x < 0 && !trailW) x = x * 0.15;
      tx.value = x;
      const full = width * FULL;
      const arm = (x > full && leadW > 0) || (x < -full && trailW > 0) ? 1 : 0;
      if (arm !== armedTarget.value) {
        armedTarget.value = arm;
        armed.value = withTiming(arm, { duration: 160 });
        runOnJS(tick)();
      }
    })
    .onEnd((e) => {
      const x = tx.value;
      if (armedTarget.value === 1) {
        const side = x > 0 ? "leading" : "trailing";
        tx.value = withTiming(x > 0 ? width : -width, { duration: 160 }, () => {
          runOnJS(fire)(side);
        });
        return;
      }
      const v = e.velocityX;
      if (x > 0 && leadW && (x > leadW / 2 || v > 600) && v > -300) {
        tx.value = withSpring(leadW, SPRING);
        runOnJS(settle)(true);
      } else if (x < 0 && trailW && (x < -trailW / 2 || v < -600) && v < 300) {
        tx.value = withSpring(-trailW, SPRING);
        runOnJS(settle)(true);
      } else {
        tx.value = withSpring(0, SPRING);
        runOnJS(settle)(false);
      }
    });

  const tapAction = (a: SwipeAction) => {
    close();
    a.onPress();
  };

  const rowStyle = useAnimatedStyle(() => ({ transform: [{ translateX: tx.value }] }));

  return (
    <View style={[styles.wrap, { width }]}>
      {leading.length > 0 && (
        <ActionRail side="leading" actions={leading} tx={tx} armed={armed} onPress={tapAction} />
      )}
      {trailing.length > 0 && (
        <ActionRail side="trailing" actions={trailing} tx={tx} armed={armed} onPress={tapAction} />
      )}
      <GestureDetector gesture={pan}>
        <Animated.View style={rowStyle}>
          {children}
          {open && (
            <Pressable
              style={StyleSheet.absoluteFill}
              onPress={close}
              accessibilityLabel="Close actions"
            />
          )}
        </Animated.View>
      </GestureDetector>
    </View>
  );
}

/** The buttons behind one side of the row. */
function ActionRail({
  side,
  actions,
  tx,
  armed,
  onPress,
}: {
  side: "leading" | "trailing";
  actions: SwipeAction[];
  tx: SharedValue<number>;
  armed: SharedValue<number>;
  onPress: (a: SwipeAction) => void;
}) {
  const lead = side === "leading";
  const railStyle = useAnimatedStyle(() => ({
    width: Math.max(0, lead ? tx.value : -tx.value),
  }));
  return (
    <Animated.View
      style={[styles.rail, lead ? { left: 0 } : { right: 0 }, railStyle]}
      pointerEvents="box-none"
    >
      {actions.map((a, i) => (
        <ActionButton
          key={a.key}
          action={a}
          index={i}
          count={actions.length}
          lead={lead}
          tx={tx}
          armed={armed}
          onPress={() => onPress(a)}
        />
      ))}
    </Animated.View>
  );
}

function ActionButton({
  action,
  index,
  count,
  lead,
  tx,
  armed,
  onPress,
}: {
  action: SwipeAction;
  index: number;
  count: number;
  lead: boolean;
  tx: SharedValue<number>;
  armed: SharedValue<number>;
  onPress: () => void;
}) {
  // Index 0 is outermost (at the screen edge). Each button gets an equal
  // share of the revealed width; once armed, the outermost takes it all.
  const style = useAnimatedStyle(() => {
    const revealed = Math.max(0, lead ? tx.value : -tx.value);
    const share = revealed / count;
    const w = interpolate(armed.value, [0, 1], [share, index === 0 ? revealed : 0]);
    const offset = interpolate(armed.value, [0, 1], [index * share, index === 0 ? 0 : revealed]);
    return lead ? { left: offset, width: w } : { right: offset, width: w };
  });
  // Label/icon hug the row-side edge of the button, like Messages.
  const contentStyle = useAnimatedStyle(() => {
    const revealed = Math.max(0, lead ? tx.value : -tx.value);
    return { opacity: interpolate(revealed, [0, ACTION_W * 0.6], [0, 1], "clamp") };
  });
  return (
    <Animated.View style={[styles.btn, { backgroundColor: action.color }, style]}>
      <Pressable
        style={[styles.btnPress, lead ? styles.btnLead : styles.btnTrail]}
        onPress={onPress}
        accessibilityRole="button"
        accessibilityLabel={action.label}
      >
        <Animated.View style={[styles.btnContent, contentStyle]}>
          <Glyph sf={action.sf} ion={action.ion} size={19} color={colors.surface} />
          <Text style={styles.btnLabel} numberOfLines={1}>
            {action.label}
          </Text>
        </Animated.View>
      </Pressable>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    overflow: "hidden",
  },
  rail: {
    position: "absolute",
    top: 0,
    bottom: 0,
    overflow: "hidden",
  },
  btn: {
    position: "absolute",
    top: 0,
    bottom: 0,
    overflow: "hidden",
  },
  btnPress: {
    flex: 1,
    justifyContent: "center",
  },
  btnLead: {
    alignItems: "flex-end",
  },
  btnTrail: {
    alignItems: "flex-start",
  },
  btnContent: {
    width: ACTION_W,
    alignItems: "center",
    gap: 4,
  },
  btnLabel: {
    ...type.footnote,
    fontWeight: "600",
    color: colors.surface,
  },
});
