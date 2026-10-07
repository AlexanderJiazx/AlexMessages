import React, { useCallback, useState } from "react";
import {
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
import { colors, type } from "../src/theme";
import { Avatar } from "../src/components/Avatar";
import { Icon, type IconName } from "../src/components/Icon";
import { GlassIconButton } from "../src/components/Glass";
import { TapMenu, type MenuAction } from "../src/components/NativeMenu";

type Page = "root" | "profile" | "account" | "notifications" | "data" | "admin";

const TITLES: Record<Page, string> = {
  root: "Settings",
  profile: "Profile",
  account: "Account",
  notifications: "Notifications",
  data: "Data",
  admin: "Admin",
};

/**
 * Settings — an iOS Settings-style inset-grouped list. The root lists the
 * profile card and the sections; each section pushes its own page inside the
 * sheet (glass back button), so nothing ever overflows on a phone.
 */
export default function SettingsScreen() {
  const s = useChatState();
  const { serverUrl, logout } = useSession();
  const router = useRouter();
  const [page, setPage] = useState<Page>("root");
  const me = s.me;

  const confirmLogout = () => {
    Alert.alert(
      "Log Out",
      "Log out from all of your devices? This ends every active session, including this one.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Log Out", style: "destructive", onPress: () => void logout() },
      ]
    );
  };

  return (
    <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
      <View style={styles.nav}>
        <View style={styles.navSide}>
          {page !== "root" && (
            <GlassIconButton
              icon="chevron-back"
              label="Back"
              onPress={() => setPage("root")}
              iconSize={22}
              size={40}
            />
          )}
        </View>
        <Text style={styles.navTitle} accessibilityRole="header">
          {TITLES[page]}
        </Text>
        <View style={[styles.navSide, { alignItems: "flex-end" }]}>
          <GlassIconButton icon="close" label="Close" onPress={() => router.back()} iconSize={20} size={40} />
        </View>
      </View>

      <ScrollView
        contentContainerStyle={styles.content}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="interactive"
      >
        {page === "root" && me && (
          <>
            <Group>
              <Pressable
                style={({ pressed }) => [styles.meCard, pressed && styles.pressed]}
                onPress={() => setPage("profile")}
                accessibilityRole="button"
                accessibilityLabel="Profile"
              >
                <Avatar user={me} size={60} />
                <View style={styles.flex}>
                  <Text style={styles.meName} numberOfLines={1}>
                    {me.display_name || me.username}
                  </Text>
                  <Text style={styles.meSub} numberOfLines={1}>
                    @{me.username} · Photo, name & bio
                  </Text>
                </View>
                <Icon name="chevron-forward" size={16} color={colors.faint} />
              </Pressable>
            </Group>

            <Group>
              <Row icon="shield-checkmark" iconBg={colors.sage} title="Account" onPress={() => setPage("account")} />
              <Row
                icon="notifications"
                iconBg="#D6694A"
                title="Notifications"
                onPress={() => setPage("notifications")}
              />
              <Row
                icon="server"
                iconBg="#5B7FA6"
                title="Data"
                onPress={() => setPage("data")}
                last={!s.isAdmin}
              />
              {s.isAdmin && (
                <Row icon="key" iconBg={colors.ink2} title="Admin" onPress={() => setPage("admin")} last />
              )}
            </Group>

            <Group>
              <Row title="Log Out" destructive center onPress={confirmLogout} last />
            </Group>

            <Text style={styles.colophon}>
              Alex Messages · {serverUrl.replace(/^https?:\/\//, "").replace(/\/+$/, "")}
            </Text>
          </>
        )}
        {page === "profile" && <ProfilePage />}
        {page === "account" && <AccountPage />}
        {page === "notifications" && <NotificationsPage />}
        {page === "data" && <DataPage />}
        {page === "admin" && <AdminPage />}
      </ScrollView>
    </SafeAreaView>
  );
}

// ---------- Profile ----------

function ProfilePage() {
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

  const photoActions: MenuAction[] = [
    { label: "Choose Photo", systemImage: "photo", onPress: () => void pickAvatar() },
    {
      label: "Remove Photo",
      systemImage: "trash",
      destructive: true,
      onPress: () => void removeAvatar(),
    },
  ];

  const save = async () => {
    const ok = await store.saveProfile(name.trim(), bio.trim());
    if (ok) {
      setName(store.state.me?.display_name || "");
      setBio(store.state.me?.bio || "");
    }
  };

  return (
    <View>
      <View style={styles.photoBlock}>
        {me.avatar ? (
          // With a photo set, tapping offers Choose / Remove as a native menu.
          <TapMenu actions={photoActions} title="Profile photo">
            <PhotoControl busy={busy} label="Edit Photo" />
          </TapMenu>
        ) : (
          <Pressable onPress={() => void pickAvatar()} accessibilityRole="button">
            <PhotoControl busy={busy} label="Add Photo" />
          </Pressable>
        )}
      </View>

      <Group header="Name">
        <TextInput
          style={styles.fieldInput}
          value={name}
          onChangeText={setName}
          placeholder="Your name"
          placeholderTextColor={colors.faint}
          maxLength={40}
          accessibilityLabel="Display name"
        />
      </Group>
      <Group header="Bio" footer="Shown on your profile to people you message.">
        <TextInput
          style={[styles.fieldInput, styles.bioInput]}
          value={bio}
          onChangeText={setBio}
          placeholder="A line or two about you…"
          placeholderTextColor={colors.faint}
          multiline
          maxLength={280}
          accessibilityLabel="Bio"
        />
      </Group>

      {dirty && (
        <Group>
          <Row title="Save Changes" tint={colors.sageDeep} center bold onPress={() => void save()} />
          <Row
            title="Discard"
            center
            tint={colors.muted}
            onPress={() => {
              setName(me.display_name || "");
              setBio(me.bio || "");
            }}
            last
          />
        </Group>
      )}
    </View>
  );
}

/** Avatar + "Edit Photo" link, the trigger for the photo menu. */
function PhotoControl({ busy, label }: { busy: boolean; label: string }) {
  const s = useChatState();
  return (
    <View style={styles.photoControl} accessibilityLabel="Change photo">
      <View>
        <Avatar user={s.me} size={104} />
        {busy && (
          <View style={styles.photoBusy}>
            <ActivityIndicator color={colors.surface} />
          </View>
        )}
      </View>
      <Text style={styles.photoLink}>{label}</Text>
    </View>
  );
}

// ---------- Account ----------

function AccountPage() {
  const { api, logout } = useSession();
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
      "Log Out",
      "Log out from all of your devices? This ends every active session, including this one.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Log Out", style: "destructive", onPress: () => void logout() },
      ]
    );
  };

  const deleteWith = async (password: string) => {
    try {
      await api.deleteAccount(password);
      toast("Account deleted");
      await logout();
    } catch (e) {
      Alert.alert("Couldn't delete", (e as { detail?: string }).detail || "Try again.");
    }
  };

  const startDelete = () => {
    if (Platform.OS === "ios") {
      Alert.prompt(
        "Delete Account",
        "Enter your password to permanently delete your account, profile, and message history. This can't be undone.",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Delete",
            style: "destructive",
            onPress: (password?: string) => {
              if (password) void deleteWith(password);
            },
          },
        ],
        "secure-text"
      );
      return;
    }
    // Android has no Alert.prompt — confirm, then ask for the password inline.
    Alert.alert(
      "Delete account",
      "Permanently delete your account, profile, and message history? This can't be undone.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Continue", style: "destructive", onPress: () => setDeleteSheet(true) },
      ]
    );
  };

  return (
    <View>
      <Group header="Account Info">
        <Row title="Username" value={`@${me.username}`} />
        <Row title="User ID" value={`#${me.id}`} />
        <Row title="Member Since" value={fmtAccountDate(s.meCreatedAt)} />
        <Row title="Status" value="Approved" valueColor={colors.sageDeep} last />
      </Group>

      <Group
        header="Change Password"
        footer={pwMsg?.text}
        footerColor={pwMsg ? (pwMsg.ok ? colors.sageDeep : colors.danger) : undefined}
      >
        <PwField
          placeholder="Current password"
          value={pw.cur}
          onChange={(v) => setPw({ ...pw, cur: v })}
          auto="current-password"
        />
        <PwField
          placeholder="New password (8–128 characters)"
          value={pw.next}
          onChange={(v) => setPw({ ...pw, next: v })}
          auto="new-password"
        />
        <PwField
          placeholder="Confirm new password"
          value={pw.confirm}
          onChange={(v) => setPw({ ...pw, confirm: v })}
          auto="new-password"
        />
        <Row
          title="Update Password"
          tint={colors.sageDeep}
          bold
          onPress={() => void submitPassword()}
          disabled={!pw.cur || !pw.next || !pw.confirm}
          last
        />
      </Group>

      <Group footer="Ends every active session, including this one.">
        <Row title="Log Out of All Devices" destructive onPress={confirmLogout} last />
      </Group>

      <Group footer="Permanently deletes your account, profile, and message history. This can't be undone.">
        <Row title="Delete Account…" destructive onPress={startDelete} last />
      </Group>

      {deleteSheet && (
        <Group header="Confirm With Your Password">
          <PwField placeholder="Password" value={deletePw} onChange={setDeletePw} auto="current-password" />
          <Row
            title="Delete Account"
            destructive
            bold
            onPress={() => {
              void deleteWith(deletePw).finally(() => {
                setDeleteSheet(false);
                setDeletePw("");
              });
            }}
          />
          <Row
            title="Cancel"
            tint={colors.muted}
            onPress={() => {
              setDeleteSheet(false);
              setDeletePw("");
            }}
            last
          />
        </Group>
      )}
    </View>
  );
}

function PwField({
  placeholder,
  value,
  onChange,
  auto,
}: {
  placeholder: string;
  value: string;
  onChange: (v: string) => void;
  auto: "current-password" | "new-password";
}) {
  return (
    <View style={styles.rowSep}>
      <TextInput
        style={styles.fieldInput}
        placeholder={placeholder}
        placeholderTextColor={colors.faint}
        secureTextEntry
        value={value}
        onChangeText={onChange}
        autoComplete={auto}
        textContentType={auto === "new-password" ? "newPassword" : "password"}
      />
    </View>
  );
}

// ---------- Notifications ----------

function NotificationsPage() {
  const [perm, setPerm] = useState<Notifications.PermissionStatus | null>(null);

  React.useEffect(() => {
    void Notifications.getPermissionsAsync().then((p) => setPerm(p.status));
  }, []);

  const enabled = perm === "granted";
  const toggle = async () => {
    if (enabled) {
      Alert.alert(
        "Notifications",
        "Notification permission is managed by the system. Open the Settings app to turn it off.",
        [{ text: "OK" }]
      );
      return;
    }
    const res = await Notifications.requestPermissionsAsync();
    setPerm(res.status);
    if (res.granted) toast("Notifications enabled");
  };

  let desc =
    "Get a notification for new direct messages while Alex Messages is in the background.";
  if (perm === "denied") {
    desc = "Blocked by the system. Enable notifications for Alex Messages in the Settings app.";
  } else if (enabled) {
    desc = "You'll get a notification when a direct message arrives in the background.";
  }

  return (
    <Group footer={desc}>
      <Row
        icon="chatbubble"
        iconBg={colors.sage}
        title="Direct message alerts"
        accessory={
          <Switch
            value={enabled}
            onValueChange={() => void toggle()}
            trackColor={{ true: colors.sage, false: colors.line }}
            accessibilityLabel="Toggle notifications"
          />
        }
        last
      />
    </Group>
  );
}

// ---------- Data ----------

function DataPage() {
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
    <Group footer="A copy of your account, contacts, and full message history as a JSON file.">
      <Row
        icon="download"
        iconBg="#5B7FA6"
        title="Export my data"
        onPress={busy ? undefined : () => void doExport()}
        accessory={busy ? <ActivityIndicator size="small" color={colors.muted} /> : undefined}
        chevron={!busy}
        last
      />
    </Group>
  );
}

// ---------- Admin ----------

function AdminPage() {
  const { serverUrl } = useSession();
  const url = serverUrl.replace(/:(\d+)$/, ":8001");
  return (
    <Group footer="You have administrator access. The control panel opens in the browser.">
      <Row
        icon="key"
        iconBg={colors.ink2}
        title="Open Admin Panel"
        onPress={() => void WebBrowser.openBrowserAsync(url)}
        accessory={<Icon name="open-outline" size={17} color={colors.faint} />}
        chevron={false}
        last
      />
    </Group>
  );
}

// ---------- grouped-list primitives ----------

function Group({
  header,
  footer,
  footerColor,
  children,
}: {
  header?: string;
  footer?: string;
  footerColor?: string;
  children: React.ReactNode;
}) {
  return (
    <View style={styles.group}>
      {header ? <Text style={styles.groupHeader}>{header}</Text> : null}
      <View style={styles.groupCard}>{children}</View>
      {footer ? (
        <Text style={[styles.groupFooter, footerColor ? { color: footerColor } : null]}>{footer}</Text>
      ) : null}
    </View>
  );
}

function Row({
  title,
  value,
  valueColor,
  icon,
  iconBg,
  onPress,
  accessory,
  chevron,
  destructive,
  tint,
  bold,
  center,
  disabled,
  last,
}: {
  title: string;
  value?: string;
  valueColor?: string;
  icon?: IconName;
  iconBg?: string;
  onPress?: () => void;
  accessory?: React.ReactNode;
  chevron?: boolean;
  destructive?: boolean;
  tint?: string;
  bold?: boolean;
  center?: boolean;
  disabled?: boolean;
  last?: boolean;
}) {
  const showChevron = chevron ?? (!!onPress && !destructive && !tint && !center);
  const color = destructive ? colors.danger : tint || colors.ink;
  return (
    <Pressable
      onPress={onPress}
      disabled={!onPress || disabled}
      style={({ pressed }) => [styles.row, pressed && onPress && styles.pressed]}
      // Static rows (values, switches) keep their children individually accessible.
      accessible={!!onPress}
      accessibilityRole={onPress ? "button" : undefined}
      accessibilityLabel={onPress ? title : undefined}
    >
      {icon && (
        <View style={[styles.rowIcon, { backgroundColor: iconBg || colors.sage }]}>
          <Icon name={icon} size={17} color={colors.surface} />
        </View>
      )}
      <View style={[styles.rowMain, !last && styles.rowSep, center && { justifyContent: "center" }]}>
        <Text
          style={[
            styles.rowTitle,
            { color },
            bold && { fontWeight: "600" },
            disabled && { opacity: 0.4 },
            center ? { textAlign: "center", flex: 1 } : { flex: 1 },
          ]}
          numberOfLines={1}
        >
          {title}
        </Text>
        {value != null && (
          <Text style={[styles.rowValue, valueColor ? { color: valueColor } : null]} numberOfLines={1}>
            {value}
          </Text>
        )}
        {accessory}
        {showChevron && <Icon name="chevron-forward" size={16} color={colors.faint} />}
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.grouped,
  },
  flex: {
    flex: 1,
  },
  nav: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingTop: 12,
    paddingBottom: 8,
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
  content: {
    paddingHorizontal: 16,
    paddingBottom: 48,
  },
  pressed: {
    backgroundColor: "rgba(27,36,31,0.06)",
  },
  group: {
    marginTop: 22,
  },
  groupHeader: {
    ...type.footnote,
    color: colors.muted,
    textTransform: "uppercase",
    marginLeft: 16,
    marginBottom: 7,
  },
  groupCard: {
    backgroundColor: colors.surface,
    borderRadius: 14,
    overflow: "hidden",
  },
  groupFooter: {
    ...type.footnote,
    color: colors.muted,
    marginHorizontal: 16,
    marginTop: 7,
    lineHeight: 18,
  },
  meCard: {
    flexDirection: "row",
    alignItems: "center",
    gap: 14,
    paddingHorizontal: 14,
    paddingVertical: 12,
  },
  meName: {
    ...type.title2,
    fontSize: 20,
    color: colors.ink,
  },
  meSub: {
    ...type.subhead,
    color: colors.muted,
    marginTop: 1,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    paddingLeft: 16,
    minHeight: 48,
  },
  rowIcon: {
    width: 30,
    height: 30,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
    marginRight: 14,
  },
  rowMain: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    minHeight: 48,
    paddingRight: 14,
    paddingVertical: 8,
  },
  rowSep: {
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.separator,
  },
  rowTitle: {
    ...type.body,
  },
  rowValue: {
    ...type.body,
    color: colors.muted,
    flexShrink: 1,
  },
  fieldInput: {
    ...type.body,
    color: colors.ink,
    paddingHorizontal: 16,
    paddingVertical: 13,
  },
  bioInput: {
    minHeight: 96,
    textAlignVertical: "top",
    paddingTop: 13,
  },
  photoBlock: {
    alignItems: "center",
    marginTop: 18,
  },
  photoControl: {
    alignItems: "center",
    gap: 10,
  },
  photoBusy: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    borderRadius: 52,
    backgroundColor: "rgba(0,0,0,0.35)",
    alignItems: "center",
    justifyContent: "center",
  },
  photoLink: {
    ...type.callout,
    fontWeight: "600",
    color: colors.sageDeep,
  },
  colophon: {
    ...type.footnote,
    color: colors.faint,
    textAlign: "center",
    marginTop: 26,
  },
});
