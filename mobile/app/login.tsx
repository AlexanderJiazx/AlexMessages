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
import { useSession } from "../src/session";
import { colors, fontDisplay, radius, shadow } from "../src/theme";

/**
 * Sign-in / register card — same structure as the web login: brand block
 * with the server host, tab switcher, and a server-URL editor (mobile needs
 * to point at the self-hosted backend).
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

  const submit = async () => {
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

  return (
    <KeyboardAvoidingView
      style={styles.kav}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScrollView contentContainerStyle={styles.scroll} keyboardShouldPersistTaps="handled">
        <View style={styles.card}>
          <View style={styles.brand}>
            <View>
              <Text style={styles.brandTitle}>Alex Messages</Text>
              <Pressable
                onPress={() => setShowServer((v) => !v)}
                accessibilityLabel={`Server ${host}`}
                accessibilityRole="button"
              >
                <View style={styles.netRow}>
                  <View style={styles.netDot} />
                  <Text style={styles.netText}>{host}</Text>
                  <Text style={styles.netEdit}>{showServer ? "▲" : "▼"}</Text>
                </View>
              </Pressable>
            </View>
          </View>

          {showServer && (
            <View style={styles.serverBox}>
              <Text style={styles.label}>Server</Text>
              <TextInput
                style={styles.input}
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

          <View style={styles.tabs}>
            <Pressable
              style={[styles.tabBtn, tab === "login" && styles.tabBtnActive]}
              onPress={() => {
                setTab("login");
                setMsg(null);
              }}
              accessibilityRole="tab"
              accessibilityState={{ selected: tab === "login" }}
            >
              <Text style={[styles.tabText, tab === "login" && styles.tabTextActive]}>Sign in</Text>
            </Pressable>
            <Pressable
              style={[styles.tabBtn, tab === "register" && styles.tabBtnActive]}
              onPress={() => {
                setTab("register");
                setMsg(null);
              }}
              accessibilityRole="tab"
              accessibilityState={{ selected: tab === "register" }}
            >
              <Text style={[styles.tabText, tab === "register" && styles.tabTextActive]}>
                Register
              </Text>
            </Pressable>
          </View>

          <View style={styles.form}>
            <Text style={styles.label}>Username</Text>
            <TextInput
              style={styles.input}
              value={username}
              onChangeText={setUsername}
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete="username"
              textContentType="username"
              accessibilityLabel="Username"
              testID="login-username"
            />
            {tab === "register" && (
              <>
                <Text style={styles.label}>Display name</Text>
                <TextInput
                  style={styles.input}
                  value={displayName}
                  onChangeText={setDisplayName}
                  placeholder="Optional — defaults to your username"
                  placeholderTextColor={colors.faint}
                  accessibilityLabel="Display name"
                />
              </>
            )}
            <Text style={styles.label}>Password</Text>
            <TextInput
              style={styles.input}
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              autoComplete={tab === "login" ? "current-password" : "new-password"}
              textContentType={tab === "login" ? "password" : "newPassword"}
              accessibilityLabel="Password"
              testID="login-password"
              onSubmitEditing={submit}
            />
            <Pressable
              style={[styles.submit, busy && { opacity: 0.6 }]}
              onPress={submit}
              disabled={busy}
              accessibilityRole="button"
              accessibilityLabel={tab === "login" ? "Sign in" : "Request account"}
            >
              {busy ? (
                <ActivityIndicator color={colors.surface} />
              ) : (
                <Text style={styles.submitText}>
                  {tab === "login" ? "Sign in" : "Request account"}
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

          {msg && (
            <Text style={[styles.msg, msg.kind === "err" ? styles.msgErr : styles.msgOk]}>
              {msg.text}
            </Text>
          )}
        </View>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  kav: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  scroll: {
    flexGrow: 1,
    alignItems: "center",
    justifyContent: "center",
    padding: 24,
  },
  card: {
    width: "100%",
    maxWidth: 420,
    backgroundColor: colors.surface,
    borderRadius: radius.l,
    padding: 26,
    ...shadow.card,
  },
  brand: {
    marginBottom: 20,
  },
  brandTitle: {
    fontFamily: fontDisplay,
    fontSize: 34,
    color: colors.ink,
  },
  netRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    marginTop: 4,
  },
  netDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: colors.sage,
  },
  netText: {
    fontSize: 13,
    color: colors.muted,
  },
  netEdit: {
    fontSize: 10,
    color: colors.faint,
  },
  serverBox: {
    marginBottom: 8,
  },
  tabs: {
    flexDirection: "row",
    backgroundColor: colors.sageTint,
    borderRadius: radius.m,
    padding: 4,
    marginBottom: 18,
  },
  tabBtn: {
    flex: 1,
    paddingVertical: 9,
    borderRadius: radius.m - 2,
    alignItems: "center",
  },
  tabBtnActive: {
    backgroundColor: colors.surface,
    ...shadow.card,
  },
  tabText: {
    fontSize: 14,
    fontWeight: "600",
    color: colors.muted,
  },
  tabTextActive: {
    color: colors.ink,
  },
  form: {},
  label: {
    fontSize: 12,
    fontWeight: "700",
    letterSpacing: 0.5,
    textTransform: "uppercase",
    color: colors.muted,
    marginBottom: 6,
    marginTop: 12,
  },
  input: {
    backgroundColor: colors.paper,
    borderRadius: radius.s,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 16,
    color: colors.ink,
  },
  submit: {
    marginTop: 20,
    backgroundColor: colors.sage,
    borderRadius: radius.pill,
    paddingVertical: 14,
    alignItems: "center",
  },
  submitText: {
    color: colors.surface,
    fontSize: 16,
    fontWeight: "700",
  },
  hint: {
    marginTop: 14,
    fontSize: 12.5,
    lineHeight: 18,
    color: colors.muted,
    textAlign: "center",
  },
  msg: {
    marginTop: 16,
    fontSize: 13.5,
    textAlign: "center",
    paddingHorizontal: 8,
  },
  msgErr: {
    color: colors.danger,
  },
  msgOk: {
    color: colors.sage,
  },
});
