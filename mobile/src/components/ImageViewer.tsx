import React from "react";
import { Modal, Pressable, StyleSheet, View } from "react-native";
import { Image } from "expo-image";
import { Icon } from "./Icon";
import { colors } from "../theme";
import { useSession } from "../session";

/** Fullscreen image viewer — the native lightbox. */
export function ImageViewer({
  url,
  onClose,
}: {
  url: string | null;
  onClose: () => void;
}) {
  const { api } = useSession();
  return (
    <Modal visible={!!url} transparent animationType="fade" onRequestClose={onClose}>
      <View style={styles.root}>
        <Pressable style={styles.backdrop} onPress={onClose} accessibilityLabel="Close" />
        {url && (
          <Image
            source={{ uri: url.startsWith("http") ? url : api.url(url) }}
            style={styles.img}
            contentFit="contain"
            accessibilityLabel="Full size image"
          />
        )}
        <Pressable style={styles.close} onPress={onClose} accessibilityLabel="Close image" hitSlop={10}>
          <Icon name="close" size={22} color={colors.surface} />
        </Pressable>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: "rgba(10,12,11,0.92)",
    alignItems: "center",
    justifyContent: "center",
  },
  backdrop: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
  },
  img: {
    width: "94%",
    height: "80%",
  },
  close: {
    position: "absolute",
    top: 56,
    right: 24,
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: "rgba(255,255,255,0.14)",
    alignItems: "center",
    justifyContent: "center",
  },
});
