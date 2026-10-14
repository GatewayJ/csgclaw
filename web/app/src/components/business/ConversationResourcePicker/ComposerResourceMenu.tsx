import { useRef, useState } from "react";
import { ComposerAddMenu, type TaskResourceKind } from "@/components/business/ConversationPane/ComposerAddMenu";
import type { TranslateFn } from "@/models/conversations";
import type { ResourceSnapshot } from "@/models/conversationResources";
import { ConversationResourcePicker } from "./ConversationResourcePicker";

export type ComposerResourceProps = {
  resourceAgentID?: string;
  onPickResources?: (resources: ResourceSnapshot[]) => void;
};

export function ComposerResourceMenu({
  resourceAgentID,
  onPickResources,
  selected = [],
  disabled,
  t,
  onAddFiles,
}: ComposerResourceProps & {
  selected?: readonly ResourceSnapshot[];
  disabled: boolean;
  t: TranslateFn;
  onAddFiles: () => void;
}) {
  const [kind, setKind] = useState<TaskResourceKind | null>(null);
  const root = useRef<HTMLDivElement>(null);
  const close = () => {
    setKind(null);
    root.current?.querySelector("button")?.focus();
  };
  return (
    <div className="relative" ref={root}>
      <ComposerAddMenu
        disabled={disabled}
        t={t}
        onAddFiles={onAddFiles}
        onOpenResourcePicker={resourceAgentID && onPickResources ? setKind : undefined}
      />
      {kind && resourceAgentID && !disabled ? (
        <ConversationResourcePicker
          key={`${resourceAgentID}:${kind}`}
          agentID={resourceAgentID}
          kind={kind}
          t={t}
          existing={selected}
          onClose={close}
          onPick={(refs) => {
            onPickResources?.(refs);
            close();
          }}
        />
      ) : null}
    </div>
  );
}
