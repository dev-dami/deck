import { type ReactNode, useState } from "react";
import { Pressable, SectionList, Text, View } from "react-native";
import type { InboxItem } from "../../../../pkg/remote/web/src/protocol3";
import type { ComputerView } from "../../../../pkg/remote/web/src/state3";
import { Button, EmptyState, Notice, ScreenHeader } from "../ui/components";
import { styles, tokens } from "../ui/theme";

export interface InboxScreenProps {
  computer: ComputerView;
  onBack(): void;
  onSelect(pj: string, sessionId: string): void;
  onNewSession(pj: string): void;
  onOpenQueue?(): void;
  children?: ReactNode;
}

export function InboxScreen({
  computer,
  onBack,
  onSelect,
  onNewSession,
  onOpenQueue,
  children,
}: InboxScreenProps) {
  const [choosing, setChoosing] = useState(false);
  const sections = (
    [
      ["Needs you", "needs_you"],
      ["Running", "running"],
      ["Done", "done"],
    ] as const
  )
    .map(([title, state]) => ({
      title,
      data: computer.inbox
        .filter((item) => item.state === state)
        .sort((a, b) => b.last_activity.localeCompare(a.last_activity)),
    }))
    .filter((section) => section.data.length);
  return (
    <View style={styles.screen}>
      <ScreenHeader
        title={computer.record.name}
        onBack={onBack}
        subtitle={
          computer.status === "online"
            ? "Your workspace, in motion."
            : "Computer offline · saved activity may be out of date"
        }
        action={onOpenQueue ? <Button title="Queue" onPress={onOpenQueue} /> : undefined}
      />
      <SectionList<InboxItem>
        sections={sections}
        keyExtractor={(item) => `${item.pj}:${item.session_id}`}
        contentContainerStyle={styles.content}
        stickySectionHeadersEnabled={false}
        ListHeaderComponent={
          <View style={{ gap: 12, marginBottom: 16 }}>
            {children}
            {computer.status !== "online" && (
              <Notice>Safe actions can wait in the queue until your computer reconnects.</Notice>
            )}
            <Button
              title="+ New conversation"
              tone="primary"
              disabled={!computer.projects.length}
              onPress={() => setChoosing(!choosing)}
            />
            {choosing &&
              computer.projects.map((project) => (
                <Button
                  key={project.id}
                  title={project.name}
                  onPress={() => {
                    setChoosing(false);
                    onNewSession(project.id);
                  }}
                />
              ))}
          </View>
        }
        ListEmptyComponent={
          <EmptyState
            title="A quiet workspace"
            detail="Start a conversation in a shared project to see its agents here."
          />
        }
        renderSectionHeader={({ section }) => (
          <Text
            accessibilityRole="header"
            style={[styles.eyebrow, { paddingBottom: 12, paddingTop: 18 }]}
          >
            {section.title}
          </Text>
        )}
        renderItem={({ item }) => (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`${item.title}, ${item.state.replaceAll("_", " ")}${item.unread ? ", unread" : ""}`}
            onPress={() => onSelect(item.pj, item.session_id)}
            style={[styles.card, { marginBottom: 10 }]}
          >
            <View style={styles.between}>
              <Text numberOfLines={2} style={[styles.heading, { flex: 1 }]}>
                {item.title || "Untitled conversation"}
              </Text>
              {item.unread && <Text style={{ color: tokens.accent }}>●</Text>}
            </View>
            <Text style={styles.secondary} numberOfLines={2}>
              {item.summary || "Open conversation"}
            </Text>
            <View style={styles.between}>
              <Text style={[styles.secondary, { color: tokens.muted }]}>
                {computer.projects.find((p) => p.id === item.pj)?.name ?? "Project"}
              </Text>
              {item.approvals > 0 && (
                <Text style={{ color: tokens.warning, fontSize: 12 }}>
                  {item.approvals} approval{item.approvals === 1 ? "" : "s"}
                </Text>
              )}
            </View>
          </Pressable>
        )}
      />
    </View>
  );
}
