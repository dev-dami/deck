import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import type { ChangedFile } from "../../../../pkg/remote/web/src/protocol";
import { EmptyState, IconButton, Notice, TopBar } from "../ui/components";
import { diffRows } from "../ui/diff-rows";
import { Icon } from "../ui/icons";
import { splitPath } from "../ui/tasks";
import { mono, useTheme } from "../ui/theme";
import { rgba } from "../ui/themes";

const MAX_DIFF = 32_000;

export function ChangesScreen({
  files,
  loading,
  error,
  onBack,
}: {
  files: ChangedFile[];
  loading: boolean;
  error: string | null;
  onBack(): void;
}) {
  const { t, s } = useTheme();
  const [open, setOpen] = useState(0);
  const added = files.reduce((n, f) => n + f.added, 0);
  const removed = files.reduce((n, f) => n + f.removed, 0);
  const tone = (kind: "add" | "del" | "ctx") => {
    if (kind === "add")
      return { bg: rgba(t.success, t.dark ? 0.12 : 0.1), mark: t.success, sign: "+" };
    if (kind === "del")
      return { bg: rgba(t.danger, t.dark ? 0.12 : 0.09), mark: t.danger, sign: "−" };
    return { bg: "transparent", mark: t.muted, sign: "" };
  };
  return (
    <View style={s.screen}>
      <TopBar
        title="Changes"
        subtitle={
          <Text style={{ fontSize: 12, fontFamily: mono, color: t.muted }}>
            {files.length} file{files.length === 1 ? "" : "s"} ·{" "}
            <Text style={{ color: t.success }}>+{added}</Text>{" "}
            <Text style={{ color: t.danger }}>−{removed}</Text>
          </Text>
        }
        left={<IconButton icon="back" label="Back to conversation" onPress={onBack} />}
      />
      <ScrollView
        contentContainerStyle={{ padding: 16, paddingTop: 8, gap: 12, paddingBottom: 40 }}
      >
        {error && <Notice error>{error}</Notice>}
        {!loading && files.length === 0 && (
          <EmptyState title="No changes yet" detail="Files your agents change show up here." />
        )}
        {files.map((file, i) => {
          const { name, dir } = splitPath(file.path);
          const expanded = open === i;
          return (
            <View key={file.path} style={[s.card, { borderRadius: 20, overflow: "hidden" }]}>
              <Pressable
                accessibilityRole="button"
                accessibilityState={{ expanded }}
                accessibilityLabel={`${file.path}, ${file.added} added, ${file.removed} removed`}
                onPress={() => setOpen(expanded ? -1 : i)}
                style={{
                  flexDirection: "row",
                  alignItems: "center",
                  gap: 10,
                  minHeight: 54,
                  paddingHorizontal: 14,
                }}
              >
                <View style={{ transform: [{ rotate: expanded ? "90deg" : "0deg" }] }}>
                  <Icon name="chevron-right" color={t.muted} size={14} weight={2.2} />
                </View>
                <View style={{ flex: 1, minWidth: 0 }}>
                  <Text
                    numberOfLines={1}
                    style={{ fontFamily: mono, fontSize: 14, fontWeight: "600", color: t.fg }}
                  >
                    {name}
                  </Text>
                  {dir !== "" && (
                    <Text numberOfLines={1} style={s.small}>
                      {dir}
                    </Text>
                  )}
                </View>
                <Text style={{ fontFamily: mono, fontSize: 12 }}>
                  <Text style={{ color: t.success }}>+{file.added}</Text>{" "}
                  <Text style={{ color: t.danger }}>−{file.removed}</Text>
                </Text>
              </Pressable>
              {expanded && (
                <View style={[s.divider, { paddingBottom: 10 }]}>
                  {file.diff ? (
                    <ScrollView horizontal showsHorizontalScrollIndicator={false}>
                      <View style={{ minWidth: "100%" }}>
                        {diffRows(file.diff.slice(0, MAX_DIFF)).map((row, n) => {
                          if (row.k === "fold") {
                            return (
                              <Text
                                key={`${n}:${row.k}:${row.skipped}`}
                                style={{
                                  paddingHorizontal: 14,
                                  fontFamily: mono,
                                  fontSize: 11,
                                  lineHeight: 21,
                                  color: t.muted,
                                  backgroundColor: rgba(t.info, 0.06),
                                }}
                              >
                                {row.skipped === null ? "⋯" : `⋯ ${row.skipped} unchanged lines`}
                              </Text>
                            );
                          }
                          if (row.k === "meta") {
                            return (
                              <Text
                                key={`${n}:${row.text}`}
                                style={{
                                  paddingHorizontal: 14,
                                  fontFamily: mono,
                                  fontSize: 12,
                                  lineHeight: 21,
                                  color: t.muted,
                                }}
                              >
                                {row.text}
                              </Text>
                            );
                          }
                          const c = tone(row.kind);
                          return (
                            <View
                              key={`${n}:${row.text}`}
                              style={{ flexDirection: "row", backgroundColor: c.bg }}
                            >
                              <Text
                                style={{
                                  width: 40,
                                  paddingRight: 6,
                                  textAlign: "right",
                                  fontFamily: mono,
                                  fontSize: 11,
                                  lineHeight: 21,
                                  color: t.muted,
                                }}
                              >
                                {row.num ?? ""}
                              </Text>
                              <Text
                                style={{
                                  width: 18,
                                  textAlign: "center",
                                  fontFamily: mono,
                                  fontSize: 12,
                                  lineHeight: 21,
                                  color: c.mark,
                                }}
                              >
                                {c.sign}
                              </Text>
                              <Text
                                selectable
                                style={{
                                  paddingRight: 14,
                                  fontFamily: mono,
                                  fontSize: 12,
                                  lineHeight: 21,
                                  color: t.soft,
                                }}
                              >
                                {row.text || " "}
                              </Text>
                            </View>
                          );
                        })}
                      </View>
                    </ScrollView>
                  ) : (
                    <Text style={[s.small, { padding: 14 }]}>No preview for this file.</Text>
                  )}
                  {(file.diff_truncated || (file.diff?.length ?? 0) > MAX_DIFF) && (
                    <Text style={[s.small, { paddingHorizontal: 14, paddingTop: 8 }]}>
                      Diff cut short. Open it on your computer to see all of it.
                    </Text>
                  )}
                </View>
              )}
            </View>
          );
        })}
      </ScrollView>
    </View>
  );
}
