import React, { useEffect, useState } from "react";
import {
  ActionSheetIOS,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { colors, radius, shadow } from "../theme";

export interface SheetOption {
  label: string;
  destructive?: boolean;
  onPress: () => void;
}

interface SheetConfig {
  title?: string;
  message?: string;
  options: SheetOption[];
  cancelLabel?: string;
}

let presenter: ((c: SheetConfig) => void) | null = null;

/**
 * Cross-platform action menu. iOS uses the native ActionSheetIOS; Android
 * renders a flat Material bottom sheet (Alert is limited to three buttons and
 * silently drops the rest, so it can't host a menu + Cancel).
 */
export function showActionSheet(cfg: SheetConfig) {
  const cancelLabel = cfg.cancelLabel ?? "Cancel";
  if (Platform.OS === "ios") {
    const destructive = cfg.options.findIndex((o) => o.destructive);
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options: [...cfg.options.map((o) => o.label), cancelLabel],
        cancelButtonIndex: cfg.options.length,
        destructiveButtonIndex: destructive >= 0 ? destructive : undefined,
        title: cfg.title,
        message: cfg.message,
      },
      (i) => {
        if (i < cfg.options.length) cfg.options[i].onPress();
      }
    );
    return;
  }
  presenter?.(cfg);
}

/** Mount once near the app root — renders the Android bottom sheet on demand. */
export function ActionSheetHost() {
  const [cfg, setCfg] = useState<SheetConfig | null>(null);
  useEffect(() => {
    presenter = setCfg;
    return () => {
      presenter = null;
    };
  }, []);
  if (Platform.OS === "ios" || !cfg) return null;
  const close = () => setCfg(null);
  const cancelLabel = cfg.cancelLabel ?? "Cancel";
  return (
    <Modal transparent animationType="slide" visible onRequestClose={close} statusBarTranslucent>
      <View style={styles.fill}>
        <Pressable style={styles.backdrop} onPress={close} accessibilityLabel="Dismiss" />
        <View style={styles.card}>
          {!!cfg.title && <Text style={styles.title}>{cfg.title}</Text>}
          {!!cfg.message && (
            <Text style={styles.message} numberOfLines={2}>
              {cfg.message}
            </Text>
          )}
          {cfg.options.map((o) => (
            <Pressable
              key={o.label}
              style={({ pressed }) => [styles.option, pressed && styles.pressed]}
              onPress={() => {
                close();
                o.onPress();
              }}
              accessibilityRole="button"
              accessibilityLabel={o.label}
            >
              <Text style={[styles.optionText, o.destructive && styles.destructive]}>
                {o.label}
              </Text>
            </Pressable>
          ))}
          <Pressable
            style={({ pressed }) => [styles.option, styles.cancel, pressed && styles.pressed]}
            onPress={close}
            accessibilityRole="button"
            accessibilityLabel={cancelLabel}
          >
            <Text style={styles.cancelText}>{cancelLabel}</Text>
          </Pressable>
        </View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  fill: {
    flex: 1,
    justifyContent: "flex-end",
  },
  backdrop: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    backgroundColor: colors.overlay,
  },
  card: {
    backgroundColor: colors.paper,
    borderTopLeftRadius: radius.l,
    borderTopRightRadius: radius.l,
    paddingTop: 12,
    paddingBottom: 30,
    paddingHorizontal: 10,
    ...shadow.float,
  },
  title: {
    fontSize: 17,
    fontWeight: "700",
    color: colors.ink,
    paddingHorizontal: 12,
    paddingBottom: 2,
  },
  message: {
    fontSize: 13,
    color: colors.muted,
    paddingHorizontal: 12,
    paddingBottom: 8,
  },
  option: {
    paddingVertical: 13,
    paddingHorizontal: 12,
    borderRadius: radius.s,
  },
  pressed: {
    backgroundColor: colors.sageTint,
  },
  optionText: {
    fontSize: 16,
    fontWeight: "500",
    color: colors.ink,
  },
  destructive: {
    color: colors.danger,
  },
  cancel: {
    marginTop: 6,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: colors.line,
  },
  cancelText: {
    fontSize: 16,
    fontWeight: "600",
    color: colors.muted,
  },
});
