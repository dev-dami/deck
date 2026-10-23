import { useEffect, useState } from "react";
import { ActivityIndicator, ScrollView, View } from "react-native";
import type { ModelChoice } from "../../../../pkg/remote/web/src/protocol";
import { Group, Notice, Row, Sheet } from "../ui/components";
import { Icon } from "../ui/icons";
import { useTheme } from "../ui/theme";

// Picks the model a new agent starts with; null keeps the computer's default.
export function ModelPicker({
  visible,
  kind,
  selected,
  load,
  onClose,
  onPick,
}: {
  visible: boolean;
  kind: string;
  selected?: string;
  load(): Promise<ModelChoice[]>;
  onClose(): void;
  onPick(model: ModelChoice | null): void;
}) {
  const { t } = useTheme();
  const [models, setModels] = useState<ModelChoice[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: load is a new closure each render; the sheet reloads per kind.
  useEffect(() => {
    if (!visible) return;
    let active = true;
    setModels(null);
    setError(null);
    void load()
      .then((found) => {
        if (active) setModels(found);
      })
      .catch((failure: unknown) => {
        if (!active) return;
        setModels([]);
        setError(failure instanceof Error ? failure.message : "Could not load models.");
      });
    return () => {
      active = false;
    };
  }, [visible, kind]);
  const check = <Icon name="check" color={t.primary} size={18} weight={2.4} />;
  return (
    <Sheet visible={visible} onClose={onClose} title={`${kind} model`}>
      <ScrollView style={{ flexGrow: 0 }} contentContainerStyle={{ gap: 12, paddingBottom: 4 }}>
        {error && <Notice error>{error}</Notice>}
        <Group>
          <Row
            title="Default model"
            subtitle="Whatever this computer is set to use"
            onPress={() => onPick(null)}
            trailing={selected === undefined ? check : undefined}
          />
          {models === null ? (
            <View style={{ padding: 20 }}>
              <ActivityIndicator color={t.primary} />
            </View>
          ) : (
            models.map((m) => (
              <Row
                key={m.id}
                divider
                title={m.label}
                subtitle={m.efforts?.length ? `Effort: ${m.efforts.join(", ")}` : undefined}
                onPress={() => onPick(m)}
                trailing={m.id === selected ? check : undefined}
              />
            ))
          )}
        </Group>
      </ScrollView>
    </Sheet>
  );
}
