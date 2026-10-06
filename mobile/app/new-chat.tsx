import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { useSession } from "../src/session";
import { colors, fontDisplay, radius } from "../src/theme";
import { Icon } from "../src/components/Icon";

/**
 * New-conversation sheet: username lookup → open the DM, then route into it
 * (wide selects in place; narrow pushes /conversation).
 */
export default function NewChat() {
  const { store } = useSession();
  const router = useRouter();
  const { wide } = useLocalSearchParams<{ wide?: string }>();
  const [username, setUsername] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const open = useCallback(async () => {
    const uname = username.trim().replace(/^@/, "");
    if (!uname || busy) return;
    setBusy(true);
    setError(null);
    const res = await store.lookupAndOpen(uname);
    setBusy(false);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    const channel = store.state.activeChannel;
    router.back();
    if (channel && wide !== "1") {
      // Small delay so the modal dismisses before the push.
      setTimeout(() => router.push({ pathname: "/conversation", params: { channel } }), 60);
    }
  }, [username, busy, store, router, wide]);

  return (
    <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
      <View style={styles.header}>
        <Text style={styles.title}>New conversation</Text>
        <Pressable onPress={() => router.back()} accessibilityLabel="Close" hitSlop={10}>
          <Icon name="close" size={22} color={colors.muted} />
        </Pressable>
      </View>
      <Text style={styles.sub}>Enter the username of the person you want to message.</Text>
      <View style={styles.inputRow}>
        <Text style={styles.at}>@</Text>
        <TextInput
          style={styles.input}
          value={username}
          onChangeText={setUsername}
          autoCapitalize="none"
          autoCorrect={false}
          autoFocus
          placeholder="username"
          placeholderTextColor={colors.faint}
          onSubmitEditing={open}
          accessibilityLabel="Username"
        />
        <Pressable
          style={[styles.go, (!username.trim() || busy) && { opacity: 0.5 }]}
          onPress={open}
          disabled={!username.trim() || busy}
          accessibilityLabel="Open conversation"
          accessibilityRole="button"
        >
          {busy ? (
            <ActivityIndicator size="small" color={colors.surface} />
          ) : (
            <Icon name="arrow-forward" size={18} color={colors.surface} />
          )}
        </Pressable>
      </View>
      {error && <Text style={styles.error}>{error}</Text>}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.paper,
    padding: 24,
    paddingTop: 20,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 6,
  },
  title: {
    fontFamily: fontDisplay,
    fontSize: 28,
    color: colors.ink,
  },
  sub: {
    fontSize: 14,
    color: colors.muted,
    marginBottom: 20,
  },
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  at: {
    fontSize: 18,
    color: colors.faint,
    fontWeight: "600",
  },
  input: {
    flex: 1,
    backgroundColor: colors.surface,
    borderRadius: radius.s,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 16,
    color: colors.ink,
  },
  go: {
    width: 44,
    height: 44,
    borderRadius: 22,
    backgroundColor: colors.sage,
    alignItems: "center",
    justifyContent: "center",
  },
  error: {
    marginTop: 14,
    color: colors.danger,
    fontSize: 13.5,
  },
});
