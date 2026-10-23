import { type ReactNode, useState } from "react";
import { Pressable, SectionList, Text, View } from "react-native";
import type { InboxItem } from "../../../../pkg/remote/web/src/protocol3";
import type { ComputerView } from "../../../../pkg/remote/web/src/state3";
import { relativeTime } from "../../../../pkg/remote/web/src/time";
import { Button, EmptyState, Notice, ScreenHeader, StatusDot } from "../ui/components";
import { useTheme } from "../ui/ThemeContext";
import { groupByProject, type ProjectGroup, threadStatus } from "../ui/threads";

export interface InboxScreenProps {
  computer: ComputerView;
  onBack(): void;
  onSelect(pj: string, sessionId: string): void;
  onNewSession(pj: string): void;
  onOpenQueue?(): void;
  children?: ReactNode;
}

interface Section {
  group: ProjectGroup;
  data: InboxItem[];
}

const STATUS_LABEL = { approval: "needs approval", working: "working", done: "unread" } as const;

function ThreadRow({ item, onPress }: { item: InboxItem; onPress(): void }) {
  const { styles, tokens } = useTheme();
  const status = threadStatus(item);
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={`${item.title || "Untitled conversation"}${status ? `, ${STATUS_LABEL[status]}` : ""}`}
      onPress={onPress}
      style={({ pressed }) => ({
        paddingVertical: 10,
        paddingHorizontal: 12,
        borderRadius: 10,
        gap: 3,
        minHeight: 52,
        backgroundColor: pressed ? tokens.elevated : "transparent",
      })}
    >
      <View style={styles.row}>
        <StatusDot status={status} />
        <Text
          numberOfLines={1}
          style={[
            styles.body,
            { flex: 1, fontWeight: status === "done" ? "700" : "500", lineHeight: 21 },
          ]}
        >
          {item.title || "Untitled conversation"}
        </Text>
        {item.approvals > 0 ? (
          <Text style={{ color: tokens.warning, fontSize: 12, fontWeight: "600" }}>
            {item.approvals} approval{item.approvals === 1 ? "" : "s"}
          </Text>
        ) : (
          <Text style={[styles.secondary, { fontSize: 12 }]}>
            {relativeTime(item.last_activity, Date.now())}
          </Text>
        )}
      </View>
      {!!item.summary && (
        <Text
          style={[styles.secondary, { fontSize: 13, paddingLeft: status ? 20 : 0 }]}
          numberOfLines={1}
        >
          {item.summary}
        </Text>
      )}
    </Pressable>
  );
}

export function InboxScreen({
  computer,
  onBack,
  onSelect,
  onNewSession,
  onOpenQueue,
  children,
}: InboxScreenProps) {
  const { styles, tokens } = useTheme();
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(new Set());
  const toggle = (set: ReadonlySet<string>, id: string): Set<string> => {
    const next = new Set(set);
    if (!next.delete(id)) next.add(id);
    return next;
  };
  const groups = groupByProject(computer.inbox, computer.projects, Date.now());
  const sections: Section[] = groups.map((group) => ({
    group,
    data: collapsed.has(group.id)
      ? []
      : [...group.live, ...(unfolded.has(group.id) ? group.settled : [])],
  }));
  const hasThreads = computer.inbox.length > 0;
  return (
    <View style={styles.screen}>
      <ScreenHeader
        title={computer.record.name}
        onBack={onBack}
        subtitle={
          computer.status === "online"
            ? "Threads grouped by project, as on your desktop."
            : "Computer offline · saved activity may be out of date"
        }
        action={onOpenQueue ? <Button title="Queue" onPress={onOpenQueue} compact /> : undefined}
      />
      <SectionList<InboxItem, Section>
        sections={sections}
        keyExtractor={(item) => `${item.pj}:${item.session_id}`}
        contentContainerStyle={[styles.content, { paddingTop: 0 }]}
        stickySectionHeadersEnabled={false}
        ListHeaderComponent={
          <View style={{ gap: 12, marginBottom: 8 }}>
            {children}
            {computer.status !== "online" && (
              <Notice>Safe actions can wait in the queue until your computer reconnects.</Notice>
            )}
          </View>
        }
        ListEmptyComponent={
          hasThreads || computer.projects.length ? null : (
            <EmptyState
              title="A quiet workspace"
              detail="Share a project from your computer to start a conversation here."
            />
          )
        }
        renderSectionHeader={({ section }) => {
          const { group } = section;
          const open = !collapsed.has(group.id);
          const total = group.live.length + group.settled.length;
          return (
            <View style={[styles.between, { paddingTop: 18, paddingBottom: 6 }]}>
              <Pressable
                accessibilityRole="button"
                accessibilityState={{ expanded: open }}
                accessibilityLabel={`${group.name}, ${total} thread${total === 1 ? "" : "s"}${group.needsYou ? `, ${group.needsYou} need you` : ""}`}
                onPress={() => setCollapsed(toggle(collapsed, group.id))}
                style={[styles.row, { flex: 1, minHeight: 36, gap: 8 }]}
              >
                <Text style={{ color: tokens.muted, fontSize: 12, width: 12 }}>
                  {open ? "▾" : "▸"}
                </Text>
                <Text
                  numberOfLines={1}
                  style={[styles.heading, { flexShrink: 1, fontSize: 14, color: tokens.secondary }]}
                >
                  {group.name}
                </Text>
                {group.needsYou > 0 && (
                  <Text style={{ color: tokens.warning, fontSize: 12, fontWeight: "600" }}>
                    {group.needsYou} need you
                  </Text>
                )}
              </Pressable>
              <Button
                compact
                tone="ghost"
                title="+ New"
                label={`New conversation in ${group.name}`}
                disabled={!group.online}
                onPress={() => onNewSession(group.id)}
              />
            </View>
          );
        }}
        renderSectionFooter={({ section }) => {
          const { group } = section;
          if (collapsed.has(group.id)) return null;
          if (!group.live.length && !group.settled.length)
            return (
              <Text style={[styles.secondary, { paddingHorizontal: 12, paddingBottom: 8 }]}>
                No conversations yet.
              </Text>
            );
          if (!group.settled.length) return null;
          const open = unfolded.has(group.id);
          return (
            <Pressable
              accessibilityRole="button"
              onPress={() => setUnfolded(toggle(unfolded, group.id))}
              style={{ paddingHorizontal: 12, paddingVertical: 10, minHeight: 44 }}
            >
              <Text style={[styles.secondary, { fontSize: 13 }]}>
                {open ? "Hide" : "Show"} {group.settled.length} earlier
              </Text>
            </Pressable>
          );
        }}
        renderItem={({ item }) => (
          <ThreadRow item={item} onPress={() => onSelect(item.pj, item.session_id)} />
        )}
      />
    </View>
  );
}
