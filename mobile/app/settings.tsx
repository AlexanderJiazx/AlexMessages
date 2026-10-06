import React, { useCallback, useState } from "react";
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import * as ImagePicker from "expo-image-picker";
import * as FileSystem from "expo-file-system";
import * as Sharing from "expo-sharing";
import * as WebBrowser from "expo-web-browser";
import * as Notifications from "expo-notifications";
import { fmtAccountDate } from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../src/session";
import { colors, fontDisplay, radius } from "../src/theme";
import { Avatar } from "../src/components/Avatar";
import { Icon, type IconName } from "../src/components/Icon";

type TabId = "profile" | "account" | "notifications" | "data" | "admin";

/**
 * Settings — the Apple-System-Settings overlay: vertical tab rail (wide) or
 * a horizontal tab strip (narrow), one panel per tab. Same five sections as
 * the web client.
 */
export default function SettingsScreen() {
  const s = useChatState();
  const router = useRouter();
  const [tab, setTab] = useState<TabId>("profile");

  const tabs: { id: TabId; label: string; icon: IconName }[] = [
    { id: "profile", label: "Profile", icon: "person-outline" },
    { id: "account", label: "Account", icon: "shield-checkmark-outline" },
    { id: "notifications", label: "Notifications", icon: "notifications-outline" },
    { id: "data", label: "Data", icon: "server-outline" },
    ...(s.isAdmin
      ? [{ id: "admin" as TabId, label: "Admin", icon: "lock-closed-outline" as IconName }]
      : []),
  ];

  return (
    <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
      <View style={styles.header}>
        <Text style={styles.title}>Settings</Text>
        <Pressable onPress={() => router.back()} accessibilityLabel="Close" hitSlop={10}>
          <Icon name="close" size={22} color={colors.muted} />
        </Pressable>
      </View>
      <View style={styles.tabs}>
        {tabs.map((t) => (
          <Pressable
            key={t.id}
            style={[styles.tabBtn, tab === t.id && styles.tabBtnActive]}
            onPress={() => setTab(t.id)}
            accessibilityRole="tab"
            accessibilityLabel={t.label}
            accessibilityState={{ selected: tab === t.id }}
          >
            <Icon name={t.icon} size={16} color={tab === t.id ? colors.sageDeep : colors.muted} />
            <Text style={[styles.tabText, tab === t.id && styles.tabTextActive]}>{t.label}</Text>
          </Pressable>
        ))}
      </View>
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        {tab === "profile" && <ProfileTab />}
        {tab === "account" && <AccountTab />}
        {tab === "notifications" && <NotificationsTab />}
        {tab === "data" && <DataTab />}
        {tab === "admin" && <AdminTab />}
      </ScrollView>
    </SafeAreaView>
  );
}

// ---------- Profile tab ----------

function ProfileTab() {
  const { api, store } = useSession();
  const s = useChatState();
  const me = s.me!;
  const [name, setName] = useState(me.display_name || "");
  const [bio, setBio] = useState(me.bio || "");
  const [busy, setBusy] = useState(false);
  const dirty = name.trim() !== (me.display_name || "") || bio.trim() !== (me.bio || "");

  const pickAvatar = useCallback(async () => {
    const res = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ["images"],
      allowsEditing: true,
      aspect: [1, 1],
      quality: 0.9,
    });
    if (res.canceled || !res.assets[0]) return;
    const asset = res.assets[0];
    if (asset.fileSize && asset.fileSize > 5 * 1024 * 1024) {
      toast("Photo exceeds 5MB", true);
      return;
    }
    setBusy(true);
    try {
      const j = await api.setAvatar({
        uri: asset.uri,
        name: asset.fileName || "avatar.jpg",
        type: asset.mimeType || "image/jpeg",
      });
      store.applyOwnProfile(j.user);
      toast("Photo updated");
    } catch {
      toast("Couldn't update photo", true);
    } finally {
      setBusy(false);
    }
  }, [api, store]);

  const removeAvatar = useCallback(async () => {
    setBusy(true);
    try {
      const j = await api.deleteAvatar();
      store.applyOwnProfile(j.user);
      toast("Photo removed");
    } catch {
      toast("Couldn't remove photo", true);
    } finally {
      setBusy(false);
    }
  }, [api, store]);

  const onCamPress = useCallback(() => {
    if (!me.avatar) {
      void pickAvatar();
      return;
    }
    if (Platform.OS === "ios") {
      ActionSheetIOS.showActionSheetWithOptions(
        { options: ["Upload photo", "Remove photo", "Cancel"], cancelButtonIndex: 2, destructiveButtonIndex: 1 },
        (i) => {
          if (i === 0) void pickAvatar();
          else if (i === 1) void removeAvatar();
        }
      );
    } else {
      Alert.alert("Profile photo", undefined, [
        { text: "Upload photo", onPress: () => void pickAvatar() },
        { text: "Remove photo", style: "destructive", onPress: () => void removeAvatar() },
        { text: "Cancel", style: "cancel" },
      ]);
    }
  }, [me.avatar, pickAvatar, removeAvatar]);

  const save = async () => {
    const ok = await store.saveProfile(name.trim(), bio.trim());
    if (ok) {
      setName(store.state.me?.display_name || "");
      setBio(store.state.me?.bio || "");
    }
  };

  return (
    <View>
      <Text style={styles.h}>Profile</Text>
      <View style={styles.avatarRow}>
        <Avatar user={me} size={84} />
        <Pressable style={styles.camBtn} onPress={onCamPress} accessibilityLabel="Change photo">
          {busy ? (
            <ActivityIndicator size="small" color={colors.surface} />
          ) : (
            <Icon name="camera" size={15} color={colors.surface} />
          )}
        </Pressable>
      </View>
      <TextInput
        style={styles.nameInput}
        value={name}
        onChangeText={setName}
        placeholder="Your name"
        placeholderTextColor={colors.faint}
        maxLength={40}
        accessibilityLabel="Display name"
      />
      <Text style={styles.groupLabel}>Bio</Text>
      <TextInput
        style={styles.bioInput}
        value={bio}
        onChangeText={setBio}
        placeholder="A line or two about you…"
        placeholderTextColor={colors.faint}
        multiline
        maxLength={280}
        accessibilityLabel="Bio"
      />
      {dirty && (
        <View style={styles.saveBar}>
          <Pressable
            style={styles.btnGhost}
            onPress={() => {
              setName(me.display_name || "");
              setBio(me.bio || "");
            }}
          >
            <Text style={styles.btnGhostText}>Cancel</Text>
          </Pressable>
          <Pressable style={styles.btnPrimary} onPress={() => void save()}>
            <Text style={styles.btnPrimaryText}>Save changes</Text>
          </Pressable>
        </View>
      )}
    </View>
  );
}

// ---------- Account tab ----------

function AccountTab() {
  const { api, store, logout } = useSession();
  const s = useChatState();
  const me = s.me!;
  const [pw, setPw] = useState({ cur: "", next: "", confirm: "" });
  const [pwMsg, setPwMsg] = useState<{ text: string; ok: boolean } | null>(null);
  const [deleteSheet, setDeleteSheet] = useState(false);
  const [deletePw, setDeletePw] = useState("");

  const submitPassword = async () => {
    if (pw.next.length < 8 || pw.next.length > 128) {
      setPwMsg({ text: "New password must be 8–128 characters.", ok: false });
      return;
    }
    if (pw.next !== pw.confirm) {
      setPwMsg({ text: "New passwords don't match.", ok: false });
      return;
    }
    try {
      await api.changePassword(pw.cur, pw.next);
      setPw({ cur: "", next: "", confirm: "" });
      setPwMsg({ text: "Password updated. Other devices were signed out.", ok: true });
      toast("Password updated");
    } catch (e) {
      setPwMsg({ text: (e as { detail?: string }).detail || "Couldn't update password.", ok: false });
    }
  };

  const confirmLogout = () => {
    Alert.alert(
      "Log out",
      "Log out from all of your devices? This ends every active session, including this one.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Log out", style: "destructive", onPress: () => void logout() },
      ]
    );
  };

  const confirmDelete = () => {
    Alert.prompt(
      "Delete account",
      "Enter your password to permanently delete your account, profile, and message history. This can't be undone.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: (password?: string) => {
            if (!password) return;
            void (async () => {
              try {
                await api.deleteAccount(password);
                toast("Account deleted");
                await logout();
              } catch (e) {
                Alert.alert("Couldn't delete", (e as { detail?: string }).detail || "Try again.");
              }
            })();
          },
        },
      ],
      "secure-text"
    );
  };

  const deleteFlow =
    Platform.OS === "ios"
      ? confirmDelete
      : () => {
          // Android has no Alert.prompt — show a confirmation that defers to
          // a secure input sheet.
          Alert.alert(
            "Delete account",
            "Permanently delete your account, profile, and message history? This can't be undone.",
            [
              { text: "Cancel", style: "cancel" },
              { text: "Continue", style: "destructive", onPress: () => setDeleteSheet(true) },
            ]
          );
        };

  const runDelete = async () => {
    try {
      await api.deleteAccount(deletePw);
      toast("Account deleted");
      await logout();
    } catch (e) {
      Alert.alert("Couldn't delete", (e as { detail?: string }).detail || "Try again.");
    } finally {
      setDeleteSheet(false);
      setDeletePw("");
    }
  };

  return (
    <View>
      <Text style={styles.h}>Account</Text>
      <Text style={styles.groupLabel}>Account info</Text>
      <View style={styles.card}>
        <Row k="Username" v={`@${me.username}`} />
        <Row k="User ID" v={`#${me.id}`} />
        <Row k="Member since" v={fmtAccountDate(s.meCreatedAt)} />
        <Row k="Status" v="approved" vColor={colors.sageDeep} last />
      </View>

      <Text style={styles.groupLabel}>Change password</Text>
      <View style={styles.card}>
        <TextInput
          style={styles.pwInput}
          placeholder="Current password"
          placeholderTextColor={colors.faint}
          secureTextEntry
          value={pw.cur}
          onChangeText={(v) => setPw({ ...pw, cur: v })}
          autoComplete="current-password"
        />
        <TextInput
          style={styles.pwInput}
          placeholder="New password (8–128 chars)"
          placeholderTextColor={colors.faint}
          secureTextEntry
          value={pw.next}
          onChangeText={(v) => setPw({ ...pw, next: v })}
          autoComplete="new-password"
        />
        <TextInput
          style={styles.pwInput}
          placeholder="Confirm new password"
          placeholderTextColor={colors.faint}
          secureTextEntry
          value={pw.confirm}
          onChangeText={(v) => setPw({ ...pw, confirm: v })}
          autoComplete="new-password"
        />
        {pwMsg && (
          <Text style={[styles.pwMsg, { color: pwMsg.ok ? colors.sageDeep : colors.danger }]}>
            {pwMsg.text}
          </Text>
        )}
        <Pressable style={[styles.btnPrimary, { alignSelf: "flex-start" }]} onPress={() => void submitPassword()}>
          <Text style={styles.btnPrimaryText}>Update password</Text>
        </Pressable>
      </View>

      <Text style={styles.groupLabel}>Session</Text>
      <SettingsRow
        title="Log out from all of your devices"
        desc="Ends every active session, including this one."
      >
        <Pressable style={styles.btn} onPress={confirmLogout}>
          <Text style={styles.btnText}>Log out</Text>
        </Pressable>
      </SettingsRow>

      <Text style={styles.groupLabel}>Danger zone</Text>
      <View style={styles.dangerZone}>
        <Text style={styles.dzTitle}>Delete account</Text>
        <Text style={styles.dzDesc}>
          Permanently deletes your account, profile, and message history. This can't be undone.
        </Text>
        <Pressable style={styles.btnDanger} onPress={deleteFlow}>
          <Text style={styles.btnDangerText}>Delete my account…</Text>
        </Pressable>
      </View>

      {deleteSheet && (
        <View style={styles.deleteSheet}>
          <Text style={styles.dzTitle}>Confirm with your password</Text>
          <TextInput
            style={styles.pwInput}
            placeholder="Password"
            placeholderTextColor={colors.faint}
            secureTextEntry
            value={deletePw}
            onChangeText={setDeletePw}
            autoFocus
          />
          <View style={styles.saveBar}>
            <Pressable
              style={styles.btnGhost}
              onPress={() => {
                setDeleteSheet(false);
                setDeletePw("");
              }}
            >
              <Text style={styles.btnGhostText}>Cancel</Text>
            </Pressable>
            <Pressable style={styles.btnDanger} onPress={() => void runDelete()}>
              <Text style={styles.btnDangerText}>Delete</Text>
            </Pressable>
          </View>
        </View>
      )}
    </View>
  );
}

// ---------- Notifications tab ----------

function NotificationsTab() {
  const [perm, setPerm] = useState<Notifications.PermissionStatus | null>(null);

  React.useEffect(() => {
    void Notifications.getPermissionsAsync().then((p) => setPerm(p.status));
  }, []);

  const enabled = perm === "granted";
  const toggle = async () => {
    if (enabled) {
      Alert.alert(
        "Notifications",
        "Notification permission is managed by the system. Open the OS Settings app to turn it off.",
        [{ text: "OK" }]
      );
      return;
    }
    const res = await Notifications.requestPermissionsAsync();
    setPerm(res.status);
    if (res.granted) toast("Notifications enabled");
  };

  let desc =
    "Get a system notification for new direct messages while Alex Messages is in the background.";
  if (perm === "denied") {
    desc = "Blocked by the system. Enable notifications for this app in the OS Settings app.";
  } else if (enabled) {
    desc = "You'll get a notification for new direct messages when they arrive in the background.";
  }

  return (
    <View>
      <Text style={styles.h}>Notifications</Text>
      <SettingsRow title="Direct message alerts" desc={desc}>
        <Switch
          value={enabled}
          onValueChange={() => void toggle()}
          trackColor={{ true: colors.sage, false: colors.line }}
          thumbColor={colors.surface}
          accessibilityLabel="Toggle notifications"
        />
      </SettingsRow>
    </View>
  );
}

// ---------- Data tab ----------

function DataTab() {
  const { api } = useSession();
  const [busy, setBusy] = useState(false);

  const doExport = async () => {
    setBusy(true);
    try {
      const data = await api.exportData();
      const path = `${FileSystem.Paths.cache}alex-messages-export.json`;
      const file = new FileSystem.File(path);
      file.write(JSON.stringify(data, null, 2));
      if (await Sharing.isAvailableAsync()) {
        await Sharing.shareAsync(file.uri, { mimeType: "application/json", dialogTitle: "Export my data" });
      } else {
        toast(`Saved to ${file.uri}`);
      }
    } catch {
      toast("Export failed", true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <View>
      <Text style={styles.h}>Data</Text>
      <SettingsRow
        title="Export my data"
        desc="A copy of your account, contacts, and full message history as a JSON file."
      >
        <Pressable style={styles.btn} onPress={() => void doExport()} disabled={busy}>
          {busy ? (
            <ActivityIndicator size="small" color={colors.ink2} />
          ) : (
            <>
              <Icon name="download-outline" size={14} color={colors.ink2} />
              <Text style={styles.btnText}>Export</Text>
            </>
          )}
        </Pressable>
      </SettingsRow>
    </View>
  );
}

// ---------- Admin tab ----------

function AdminTab() {
  const { serverUrl } = useSession();
  const url = serverUrl.replace(/:(\d+)$/, ":8001");
  return (
    <View>
      <Text style={styles.h}>Admin</Text>
      <Text style={styles.sub}>
        You have administrator access. The control panel opens in the browser.
      </Text>
      <Pressable
        style={[styles.btnPrimary, { alignSelf: "flex-start", marginTop: 14 }]}
        onPress={() => void WebBrowser.openBrowserAsync(url)}
      >
        <Text style={styles.btnPrimaryText}>Open admin panel</Text>
      </Pressable>
    </View>
  );
}

// ---------- shared bits ----------

function Row({ k, v, vColor, last }: { k: string; v: string; vColor?: string; last?: boolean }) {
  return (
    <View style={[styles.row, last && { borderBottomWidth: 0 }]}>
      <Text style={styles.rowK}>{k}</Text>
      <Text style={[styles.rowV, vColor ? { color: vColor } : null]}>{v}</Text>
    </View>
  );
}

function SettingsRow({
  title,
  desc,
  children,
}: {
  title: string;
  desc?: string;
  children: React.ReactNode;
}) {
  return (
    <View style={styles.settingsRow}>
      <View style={styles.settingsRowLabel}>
        <Text style={styles.settingsRowTitle}>{title}</Text>
        {desc ? <Text style={styles.settingsRowDesc}>{desc}</Text> : null}
      </View>
      {children}
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.paper,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 20,
    paddingTop: 20,
    paddingBottom: 8,
  },
  title: {
    fontFamily: fontDisplay,
    fontSize: 28,
    color: colors.ink,
  },
  tabs: {
    flexDirection: "row",
    paddingHorizontal: 14,
    gap: 6,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line,
    paddingBottom: 10,
  },
  tabBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    paddingHorizontal: 11,
    paddingVertical: 7,
    borderRadius: radius.pill,
  },
  tabBtnActive: {
    backgroundColor: colors.sageTint,
  },
  tabText: {
    fontSize: 13,
    fontWeight: "600",
    color: colors.muted,
  },
  tabTextActive: {
    color: colors.sageDeep,
  },
  content: {
    padding: 20,
    paddingBottom: 48,
  },
  h: {
    fontFamily: fontDisplay,
    fontSize: 24,
    color: colors.ink,
    marginBottom: 14,
  },
  sub: {
    fontSize: 13.5,
    color: colors.muted,
    lineHeight: 19,
  },
  avatarRow: {
    alignSelf: "center",
    marginBottom: 14,
  },
  camBtn: {
    position: "absolute",
    right: -2,
    bottom: -2,
    width: 30,
    height: 30,
    borderRadius: 15,
    backgroundColor: colors.sage,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: 2,
    borderColor: colors.paper,
  },
  nameInput: {
    backgroundColor: colors.surface,
    borderRadius: radius.s,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 16,
    color: colors.ink,
    textAlign: "center",
    fontWeight: "600",
  },
  bioInput: {
    backgroundColor: colors.surface,
    borderRadius: radius.s,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 15,
    color: colors.ink,
    minHeight: 90,
    textAlignVertical: "top",
  },
  groupLabel: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 1,
    textTransform: "uppercase",
    color: colors.faint,
    marginTop: 20,
    marginBottom: 8,
  },
  saveBar: {
    flexDirection: "row",
    justifyContent: "flex-end",
    gap: 10,
    marginTop: 14,
  },
  btnGhost: {
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: radius.pill,
  },
  btnGhostText: {
    fontSize: 14,
    fontWeight: "600",
    color: colors.muted,
  },
  btnPrimary: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: colors.sage,
    borderRadius: radius.pill,
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
  btnPrimaryText: {
    fontSize: 14,
    fontWeight: "700",
    color: colors.surface,
  },
  btn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: colors.surface,
    borderRadius: radius.pill,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
    paddingVertical: 9,
  },
  btnText: {
    fontSize: 13.5,
    fontWeight: "600",
    color: colors.ink2,
  },
  btnDanger: {
    backgroundColor: colors.danger,
    borderRadius: radius.pill,
    paddingHorizontal: 16,
    paddingVertical: 10,
    alignSelf: "flex-start",
  },
  btnDangerText: {
    fontSize: 14,
    fontWeight: "700",
    color: colors.surface,
  },
  card: {
    backgroundColor: colors.surface,
    borderRadius: radius.m,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
    paddingVertical: 6,
  },
  row: {
    flexDirection: "row",
    justifyContent: "space-between",
    paddingVertical: 11,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line2,
  },
  rowK: {
    fontSize: 13.5,
    color: colors.muted,
  },
  rowV: {
    fontSize: 13.5,
    fontWeight: "600",
    color: colors.ink,
  },
  pwInput: {
    backgroundColor: colors.paper,
    borderRadius: radius.s,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 15,
    color: colors.ink,
    marginTop: 8,
  },
  pwMsg: {
    fontSize: 12.5,
    marginTop: 8,
  },
  settingsRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingVertical: 10,
  },
  settingsRowLabel: {
    flex: 1,
  },
  settingsRowTitle: {
    fontSize: 14.5,
    fontWeight: "600",
    color: colors.ink,
  },
  settingsRowDesc: {
    fontSize: 12.5,
    color: colors.muted,
    lineHeight: 17,
    marginTop: 2,
  },
  dangerZone: {
    backgroundColor: "#FBEFEA",
    borderRadius: radius.m,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#EBD2C8",
    padding: 14,
  },
  dzTitle: {
    fontSize: 14.5,
    fontWeight: "700",
    color: colors.danger,
  },
  dzDesc: {
    fontSize: 12.5,
    color: colors.muted,
    lineHeight: 18,
    marginTop: 4,
    marginBottom: 12,
  },
  deleteSheet: {
    marginTop: 16,
    backgroundColor: colors.surface,
    borderRadius: radius.m,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    padding: 14,
  },
});
