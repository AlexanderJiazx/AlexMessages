import React, { useCallback, useState } from "react";
import { ActivityIndicator, Pressable, Text, TextInput, View } from "react-native";
import * as ImagePicker from "expo-image-picker";
import { toast, useChatState, useSession } from "../../src/session";
import { colors } from "../../src/theme";
import { Avatar } from "../../src/components/Avatar";
import { TapMenu, type MenuAction } from "../../src/components/NativeMenu";
import { Group, Row, SettingsPage, styles } from "../../src/components/SettingsList";

export default function ProfilePage() {
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
    <SettingsPage>
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
    </SettingsPage>
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
