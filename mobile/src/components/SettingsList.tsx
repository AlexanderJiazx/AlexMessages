import React from "react";
import { Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { colors, type } from "../theme";
import { Icon, type IconName } from "./Icon";

/**
 * iOS Settings-style inset-grouped list primitives shared by the settings
 * pages (app/settings/*). Each page is a native stack screen, so the native
 * header owns the title, back button, and large-title collapse.
 */
export function SettingsPage({ children }: { children: React.ReactNode }) {
  return (
    <ScrollView
      style={styles.root}
      contentContainerStyle={styles.content}
      // Lets the native header inset the content and collapse the large title.
      contentInsetAdjustmentBehavior="automatic"
      keyboardShouldPersistTaps="handled"
      keyboardDismissMode="interactive"
    >
      {children}
    </ScrollView>
  );
}

export function Group({
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

export function Row({
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

export function PwField({
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

export const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.grouped,
  },
  flex: {
    flex: 1,
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
