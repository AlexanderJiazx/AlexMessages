import React, { useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { Redirect } from "expo-router";
import { Image } from "expo-image";
import { SafeAreaView } from "react-native-safe-area-context";
import { useSession } from "../src/session";
import { colors, fontDisplay, radius, shadow, type } from "../src/theme";
import { Icon } from "../src/components/Icon";
import { GlassSurface } from "../src/components/Glass";

const LOGO = require("../assets/icon.png");

/**
 * Sign-in / register. Brand mark + serif wordmark, a glass server chip that
 * expands into the server-URL field (the app talks to a self-hosted backend),
 * an iOS segmented control, and inset-grouped fields.
 */
export default function LoginScreen() {
  const { phase, login, register, serverUrl, setServerUrl } = useSession();
  const [tab, setTab] = useState<"login" | "register">("login");
  const [msg, setMsg] = useState<{ text: string; kind: "err" | "ok" } | null>(null);
  const [busy, setBusy] = useState(false);
  const [showServer, setShowServer] = useState(false);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [serverDraft, setServerDraft] = useState(serverUrl);

  if (phase === "ready") return <Redirect href="/chat" />;

  const host = serverUrl.replace(/^https?:\/\//, "").replace(/\/+$/, "");
  const canSubmit = !!username.trim() && !!password && !busy;

  const submit = async () => {
    if (!canSubmit) return;
    setBusy(true);
    setMsg(null);
    try {
      if (showServer && serverDraft !== serverUrl) {
        await setServerUrl(serverDraft);
        setShowServer(false);
      }
      if (tab === "login") {
        const err = await login(username, password);
        if (err) setMsg({ text: err, kind: "err" });
      } else {
        const res = await register(username, displayName, password);
        if (res.ok) {
          setMsg({
            text: "Account requested. You'll be able to sign in once an admin approves it.",
            kind: "ok",
          });
          setTab("login");
          setPassword("");
        } else {
          setMsg({ text: res.error, kind: "err" });
        }
      }
    } finally {
      setBusy(false);
    }
  };

  const switchTab = (t: "login" | "register") => {
    setTab(t);
    setMsg(null);
  };

  return (
    <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
      <KeyboardAvoidingView
        style={styles.flex}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          contentContainerStyle={styles.scroll}
          keyboardShouldPersistTaps="handled"
          keyboardDismissMode="interactive"
        >
          <View style={styles.column}>
            <View style={styles.brand}>
              <View style={styles.mark}>
                <Image source={LOGO} style={styles.markImage} accessibilityIgnoresInvertColors />
              </View>
              <Text style={styles.wordmark}>Alex Messages</Text>
              <Pressable
                onPress={() => setShowServer((v) => !v)}
                accessibilityLabel={`Server ${host}`}
                accessibilityRole="button"
                hitSlop={6}
              >
                <GlassSurface interactive style={styles.serverChip}>
                  <View style={styles.serverDot} />
                  <Text style={styles.serverText} numberOfLines={1}>
                    {host}
                  </Text>
                  <Icon
                    name={showServer ? "chevron-up" : "chevron-down"}
                    size={13}
                    color={colors.muted}
                  />
                </GlassSurface>
              </Pressable>
            </View>

            {showServer && (
              <View style={styles.card}>
                <TextInput
                  style={styles.field}
                  value={serverDraft}
                  onChangeText={setServerDraft}
                  autoCapitalize="none"
                  autoCorrect={false}
                  autoComplete="off"
                  keyboardType="url"
                  clearButtonMode="while-editing"
                  placeholder="https://messages.example.com"
                  placeholderTextColor={colors.faint}
                  accessibilityLabel="Server URL"
                />
              </View>
            )}

            <View style={styles.segment} accessibilityRole="tablist">
              {(["login", "register"] as const).map((t) => (
                <Pressable
                  key={t}
                  style={[styles.segBtn, tab === t && styles.segBtnOn]}
                  onPress={() => switchTab(t)}
                  accessibilityRole="tab"
                  accessibilityState={{ selected: tab === t }}
                >
                  <Text style={[styles.segText, tab === t && styles.segTextOn]}>
                    {t === "login" ? "Sign In" : "Register"}
                  </Text>
                </Pressable>
              ))}
            </View>

            <View style={styles.card}>
              <TextInput
                style={styles.field}
                value={username}
                onChangeText={setUsername}
                placeholder="Username"
                placeholderTextColor={colors.faint}
                autoCapitalize="none"
                autoCorrect={false}
                autoComplete="username"
                textContentType="username"
                returnKeyType="next"
                accessibilityLabel="Username"
                testID="login-username"
              />
              {tab === "register" && (
                <>
                  <View style={styles.sep} />
                  <TextInput
                    style={styles.field}
                    value={displayName}
                    onChangeText={setDisplayName}
                    placeholder="Display name (optional)"
                    placeholderTextColor={colors.faint}
                    accessibilityLabel="Display name"
                  />
                </>
              )}
              <View style={styles.sep} />
              <TextInput
                style={styles.field}
                value={password}
                onChangeText={setPassword}
                placeholder="Password"
                placeholderTextColor={colors.faint}
                secureTextEntry
                autoComplete={tab === "login" ? "current-password" : "new-password"}
                textContentType={tab === "login" ? "password" : "newPassword"}
                returnKeyType="go"
                accessibilityLabel="Password"
                testID="login-password"
                onSubmitEditing={submit}
              />
            </View>

            {msg && (
              <Text style={[styles.msg, msg.kind === "err" ? styles.msgErr : styles.msgOk]}>
                {msg.text}
              </Text>
            )}

            <Pressable
              style={({ pressed }) => [
                styles.submit,
                !canSubmit && styles.submitOff,
                pressed && canSubmit && { opacity: 0.85 },
              ]}
              onPress={submit}
              disabled={!canSubmit}
              accessibilityRole="button"
              accessibilityLabel={tab === "login" ? "Sign in" : "Request account"}
              testID="login-submit"
            >
              {busy ? (
                <ActivityIndicator color={colors.surface} />
              ) : (
                <Text style={styles.submitText}>
                  {tab === "login" ? "Sign In" : "Request Account"}
                </Text>
              )}
            </Pressable>

            {tab === "register" && (
              <Text style={styles.hint}>
                New accounts are reviewed by an administrator. You'll be able to sign in once
                approved.
              </Text>
            )}
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
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
  scroll: {
    flexGrow: 1,
    justifyContent: "center",
    paddingHorizontal: 20,
    paddingVertical: 32,
  },
  column: {
    width: "100%",
    maxWidth: 420,
    alignSelf: "center",
  },
  brand: {
    alignItems: "center",
    marginBottom: 28,
  },
  mark: {
    width: 84,
    height: 84,
    borderRadius: 22,
    backgroundColor: colors.sageTint,
    marginBottom: 16,
    ...shadow.float,
    shadowColor: colors.sageDeep,
  },
  markImage: {
    width: "100%",
    height: "100%",
    borderRadius: 22,
  },
  wordmark: {
    fontFamily: fontDisplay,
    fontSize: 36,
    color: colors.ink,
    marginBottom: 12,
  },
  serverChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 7,
    height: 32,
    paddingHorizontal: 13,
    borderRadius: 16,
    maxWidth: 300,
  },
  serverDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: "#4CB86E",
  },
  serverText: {
    ...type.footnote,
    color: colors.ink2,
    flexShrink: 1,
  },
  segment: {
    flexDirection: "row",
    backgroundColor: "rgba(118,128,112,0.14)",
    borderRadius: 10,
    padding: 2,
    marginBottom: 14,
  },
  segBtn: {
    flex: 1,
    height: 34,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
  },
  segBtnOn: {
    backgroundColor: colors.surface,
    shadowColor: "#000",
    shadowOpacity: 0.1,
    shadowRadius: 4,
    shadowOffset: { width: 0, height: 2 },
    elevation: 2,
  },
  segText: {
    ...type.footnote,
    fontSize: 14,
    fontWeight: "500",
    color: colors.ink2,
  },
  segTextOn: {
    fontWeight: "600",
    color: colors.ink,
  },
  card: {
    backgroundColor: colors.surface,
    borderRadius: 14,
    marginBottom: 14,
    overflow: "hidden",
  },
  field: {
    ...type.body,
    color: colors.ink,
    paddingHorizontal: 16,
    height: 50,
  },
  sep: {
    height: StyleSheet.hairlineWidth,
    backgroundColor: colors.separator,
    marginLeft: 16,
  },
  submit: {
    height: 52,
    borderRadius: radius.pill,
    backgroundColor: colors.sage,
    alignItems: "center",
    justifyContent: "center",
    marginTop: 4,
  },
  submitOff: {
    opacity: 0.45,
  },
  submitText: {
    ...type.headline,
    color: colors.surface,
  },
  hint: {
    ...type.footnote,
    color: colors.muted,
    textAlign: "center",
    lineHeight: 18,
    marginTop: 14,
    paddingHorizontal: 12,
  },
  msg: {
    ...type.footnote,
    textAlign: "center",
    marginBottom: 12,
    paddingHorizontal: 8,
  },
  msgErr: {
    color: colors.danger,
  },
  msgOk: {
    color: colors.sageDeep,
  },
});
