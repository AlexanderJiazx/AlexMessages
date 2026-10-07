import React, { useState } from "react";
import { Alert, Platform } from "react-native";
import { fmtAccountDate } from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../../src/session";
import { colors } from "../../src/theme";
import { Group, PwField, Row, SettingsPage } from "../../src/components/SettingsList";

export default function AccountPage() {
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
    <SettingsPage>
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
    </SettingsPage>
  );
}
