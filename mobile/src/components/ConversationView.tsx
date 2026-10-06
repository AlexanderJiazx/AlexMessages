import React, { useCallback, useEffect, useState } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { dismissForChannel } from "../notify";
import { colors } from "../theme";
import { Composer } from "./Composer";
import { ImageViewer } from "./ImageViewer";
import { MessageList, Topbar } from "./MessageList";

/**
 * The conversation surface: topbar (peer + presence), reconnecting banner,
 * message stream, composer. Shared between the wide two-pane layout and the
 * narrow pushed route.
 */
export function ConversationView({
  channel,
  showBack,
  onBack,
  onOpenProfile,
}: {
  channel: string;
  showBack: boolean;
  onBack?: () => void;
  onOpenProfile: (uid: number) => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const insets = useSafeAreaInsets();
  const [viewerUrl, setViewerUrl] = useState<string | null>(null);

  // Mark the channel active for read receipts; clear its notification.
  // In the narrow layout, leaving the screen deselects the channel so new
  // messages stop auto-marking read (the list becomes the "not viewing" state).
  useEffect(() => {
    if (store.state.activeChannel !== channel) store.switchChannel(channel);
    void dismissForChannel(channel);
    return () => {
      if (showBack && store.state.activeChannel === channel) store.switchChannel(null);
    };
  }, [channel, store, showBack]);

  const peerId = store.peerOf(channel);
  const peerName = nameFor(s.users, peerId);

  const onImagePress = useCallback((url: string) => setViewerUrl(url), []);

  return (
    <View style={styles.root}>
      <Topbar
        channel={channel}
        showBack={showBack}
        onBack={onBack}
        onOpenProfile={onOpenProfile}
      />
      {s.connState === "reconnecting" && (
        <View style={styles.banner}>
          <Text style={styles.bannerText}>Reconnecting…</Text>
        </View>
      )}
      <KeyboardAvoidingView
        style={styles.flex}
        behavior={Platform.OS === "ios" ? "padding" : "height"}
        keyboardVerticalOffset={Platform.OS === "ios" ? 0 : 0}
      >
        <MessageList
          channel={channel}
          onOpenProfile={onOpenProfile}
          onImagePress={onImagePress}
        />
        <View style={{ paddingBottom: Math.max(insets.bottom, 8) }}>
          <Composer peerName={peerName} />
        </View>
      </KeyboardAvoidingView>
      <ImageViewer url={viewerUrl} onClose={() => setViewerUrl(null)} />
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  flex: {
    flex: 1,
  },
  banner: {
    backgroundColor: colors.warn,
    paddingVertical: 5,
    alignItems: "center",
  },
  bannerText: {
    color: colors.surface,
    fontSize: 12,
    fontWeight: "700",
    letterSpacing: 0.3,
  },
});
