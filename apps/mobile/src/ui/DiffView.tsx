import { useMemo, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import { Button } from "./components";
import { diffRows } from "./diff-rows";
import { useTheme } from "./ThemeContext";
import { mono } from "./theme";

const INITIAL_ROWS = 300;

// The desktop's diff panel: a line-number gutter, tinted added and removed
// lines, and a fold between hunks. Long lines scroll sideways.
export function DiffView({ diff, truncated = false }: { diff: string; truncated?: boolean }) {
  const { tokens, styles } = useTheme();
  const rows = useMemo(() => diffRows(diff), [diff]);
  const [all, setAll] = useState(false);
  const shown = all ? rows : rows.slice(0, INITIAL_ROWS);
  const gutter = String(
    Math.max(0, ...rows.map((r) => (r.k === "line" ? (r.num ?? 0) : 0))),
  ).length;
  return (
    <View
      style={{
        borderRadius: 10,
        borderWidth: 1,
        borderColor: tokens.border,
        backgroundColor: tokens.code,
        overflow: "hidden",
      }}
    >
      <ScrollView horizontal nestedScrollEnabled contentContainerStyle={{ minWidth: "100%" }}>
        <View style={{ alignSelf: "stretch", paddingVertical: 4 }}>
          {shown.map((row, index) => {
            // Rows have no stable identity of their own; the list is static per diff.
            const key = `${index}`;
            if (row.k === "fold")
              return (
                <Text
                  key={key}
                  style={{
                    fontFamily: mono,
                    fontSize: 11,
                    paddingVertical: 3,
                    paddingHorizontal: 10,
                    color: tokens.muted,
                    backgroundColor: tokens.elevated,
                  }}
                >
                  {row.skipped === null ? "⋯" : `⋯ ${row.skipped} unchanged lines`}
                </Text>
              );
            if (row.k === "meta")
              return (
                <Text
                  key={key}
                  style={{
                    fontFamily: mono,
                    fontSize: 11,
                    paddingHorizontal: 10,
                    color: tokens.muted,
                  }}
                >
                  {row.text}
                </Text>
              );
            const add = row.kind === "add";
            const del = row.kind === "del";
            return (
              <View
                key={key}
                style={{
                  flexDirection: "row",
                  backgroundColor: add ? tokens.diffAdd : del ? tokens.diffDel : "transparent",
                }}
              >
                <Text
                  style={{
                    fontFamily: mono,
                    fontSize: 11.5,
                    lineHeight: 18,
                    color: tokens.muted,
                    textAlign: "right",
                    paddingLeft: 8,
                    paddingRight: 6,
                    minWidth: gutter * 7.5 + 14,
                  }}
                >
                  {row.num ?? ""}
                </Text>
                <Text
                  style={{
                    fontFamily: mono,
                    fontSize: 12,
                    lineHeight: 18,
                    paddingRight: 12,
                    color: add ? tokens.diffAddFg : del ? tokens.diffDelFg : tokens.text,
                  }}
                >
                  {add ? "+" : del ? "−" : " "}
                  {row.text}
                </Text>
              </View>
            );
          })}
        </View>
      </ScrollView>
      {!all && rows.length > INITIAL_ROWS && (
        <View style={{ padding: 8 }}>
          <Button
            compact
            title={`Show ${rows.length - INITIAL_ROWS} more lines`}
            onPress={() => setAll(true)}
          />
        </View>
      )}
      {truncated && (
        <Text style={[styles.secondary, { padding: 10, fontSize: 12 }]}>
          Diff truncated by Deck. Review the full change on your computer.
        </Text>
      )}
    </View>
  );
}
