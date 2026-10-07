import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import type { PublicUser } from "@alexmessages/shared";
import { useChatState, useSession } from "../src/session";
import { colors, type } from "../src/theme";
import { Avatar } from "../src/components/Avatar";
import { Icon } from "../src/components/Icon";
import { GlassIconButton } from "../src/components/Glass";

/**
 * New-message sheet: a "To:" field that takes any username, with known
 * people suggested below (filtered as you type). Opening routes into the DM
 * — wide selects in place; narrow pushes /conversation.
 */
export default function NewChat() {
  const { store } = useSession();
  const s = useChatState();
  const router = useRouter();
  const { wide } = useLocalSearchParams<{ wide?: string }>();
  const [username, setUsername] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const openByName = useCallback(
    async (raw: string) => {
      const uname = raw.trim().replace(/^@/, "");
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
    },
    [busy, store, router, wide]
  );

  const q = username.trim().replace(/^@/, "").toLowerCase();
  const people = Object.values(s.users)
    .filter((u) => u.id !== s.me?.id)
    .filter(
      (u) =>
        !q ||
        u.username.toLowerCase().includes(q) ||
        (u.display_name || "").toLowerCase().includes(q)
    )
    .sort((a, b) => (a.display_name || a.username).localeCompare(b.display_name || b.username));

  return (
    <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
      <View style={styles.nav}>
        <View style={styles.navSide} />
        <Text style={styles.navTitle} accessibilityRole="header">
          New Message
        </Text>
        <View style={[styles.navSide, { alignItems: "flex-end" }]}>
          <GlassIconButton icon="close" label="Close" onPress={() => router.back()} size={40} />
        </View>
      </View>

      <View style={styles.toCard}>
        <Text style={styles.toLabel}>To:</Text>
        <TextInput
          style={styles.toInput}
          value={username}
          onChangeText={(v) => {
            setUsername(v);
            setError(null);
          }}
          autoCapitalize="none"
          autoCorrect={false}
          autoFocus
          placeholder="username"
          placeholderTextColor={colors.faint}
          returnKeyType="go"
          onSubmitEditing={() => void openByName(username)}
          accessibilityLabel="Username"
        />
        <Pressable
          style={[styles.go, (!username.trim() || busy) && styles.goOff]}
          onPress={() => void openByName(username)}
          disabled={!username.trim() || busy}
          accessibilityLabel="Open conversation"
          accessibilityRole="button"
          hitSlop={6}
        >
          {busy ? (
            <ActivityIndicator size="small" color={colors.surface} />
          ) : (
            <Icon name="arrow-up" size={18} color={colors.surface} />
          )}
        </Pressable>
      </View>
      {error && <Text style={styles.error}>{error}</Text>}

      {people.length > 0 && (
        <>
          <Text style={styles.sectionLabel}>{q ? "Matches" : "People"}</Text>
          <FlatList
            data={people}
            keyExtractor={(u) => String(u.id)}
            keyboardShouldPersistTaps="handled"
            contentContainerStyle={styles.listCard}
            style={styles.list}
            renderItem={({ item, index }) => (
              <PersonRow
                u={item}
                last={index === people.length - 1}
                onPress={() => void openByName(item.username)}
              />
            )}
          />
        </>
      )}
    </SafeAreaView>
  );
}

function PersonRow({ u, last, onPress }: { u: PublicUser; last: boolean; onPress: () => void }) {
  return (
    <Pressable
      style={({ pressed }) => [styles.person, pressed && styles.pressed]}
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={`Message ${u.display_name || u.username}`}
    >
      <Avatar user={u} size={40} />
      <View style={[styles.personBody, !last && styles.personSep]}>
        <Text style={styles.personName} numberOfLines={1}>
          {u.display_name || u.username}
        </Text>
        <Text style={styles.personHandle} numberOfLines={1}>
          @{u.username}
        </Text>
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.grouped,
  },
  nav: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingTop: 12,
    paddingBottom: 10,
  },
  navSide: {
    width: 64,
  },
  navTitle: {
    flex: 1,
    textAlign: "center",
    ...type.headline,
    color: colors.ink,
  },
  toCard: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 6,
    paddingLeft: 16,
    paddingRight: 6,
    height: 52,
    borderRadius: 14,
    backgroundColor: colors.surface,
  },
  toLabel: {
    ...type.body,
    color: colors.muted,
    marginRight: 6,
  },
  toInput: {
    flex: 1,
    ...type.body,
    color: colors.ink,
    height: "100%",
  },
  go: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: colors.sage,
    alignItems: "center",
    justifyContent: "center",
  },
  goOff: {
    opacity: 0.35,
  },
  error: {
    ...type.footnote,
    color: colors.danger,
    marginHorizontal: 32,
    marginTop: 8,
  },
  sectionLabel: {
    ...type.footnote,
    color: colors.muted,
    textTransform: "uppercase",
    marginHorizontal: 32,
    marginTop: 24,
    marginBottom: 7,
  },
  list: {
    flexGrow: 0,
  },
  listCard: {
    marginHorizontal: 16,
    borderRadius: 14,
    overflow: "hidden",
    backgroundColor: colors.surface,
  },
  pressed: {
    backgroundColor: "rgba(27,36,31,0.06)",
  },
  person: {
    flexDirection: "row",
    alignItems: "center",
    paddingLeft: 14,
  },
  personBody: {
    flex: 1,
    marginLeft: 12,
    paddingVertical: 10,
    paddingRight: 14,
  },
  personSep: {
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.separator,
  },
  personName: {
    ...type.body,
    fontWeight: "500",
    color: colors.ink,
  },
  personHandle: {
    ...type.footnote,
    color: colors.muted,
  },
});
