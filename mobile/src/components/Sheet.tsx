import React from "react";
import {
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  View,
} from "react-native";
import { colors, radius, shadow } from "../theme";

/**
 * Shared bottom-sheet wrapper: dimmed backdrop tap-to-dismiss + a rounded
 * card that hugs the bottom edge (iOS-style sheet; flat on Android).
 */
export function Sheet({
  visible,
  onClose,
  children,
  maxHeight = "88%",
}: {
  visible: boolean;
  onClose: () => void;
  children: React.ReactNode;
  maxHeight?: number | `${number}%`;
}) {
  return (
    <Modal
      visible={visible}
      transparent
      animationType="slide"
      onRequestClose={onClose}
      statusBarTranslucent
    >
      <KeyboardAvoidingView
        style={styles.kav}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <Pressable style={styles.backdrop} onPress={onClose} accessibilityLabel="Dismiss" />
        <View style={[styles.card, { maxHeight }]}>{children}</View>
      </KeyboardAvoidingView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  kav: {
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
    ...shadow.float,
  },
});
