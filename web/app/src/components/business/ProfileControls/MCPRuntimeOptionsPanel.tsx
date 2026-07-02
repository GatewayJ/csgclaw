import { useEffect, useId, useState } from "react";
import {
  MCP_RUNTIME_OPTIONS_EXAMPLE,
  mcpRuntimeOptionsText,
  parseMCPRuntimeOptionsText,
  setMCPRuntimeOptions,
  type AgentDraft,
  type JSONRecord,
} from "@/models/agents";
import type { TranslateFn } from "@/models/conversations";
import { Button } from "@/components/ui/Button/Button";

export type MCPRuntimeOptionsPanelProps = {
  draft: AgentDraft;
  onDraftChange: (draft: AgentDraft) => void;
  onInvalidChange?: (invalid: boolean) => void;
  t: TranslateFn;
};

function cloneMCPExample(): JSONRecord {
  return JSON.parse(JSON.stringify(MCP_RUNTIME_OPTIONS_EXAMPLE)) as JSONRecord;
}

function errorMessageForKey(key: "invalid_json" | "object_required", t: TranslateFn): string {
  return key === "invalid_json" ? t("profileMCPServersInvalidJSON") : t("profileMCPServersObjectRequired");
}

export function MCPRuntimeOptionsPanel({ draft, onDraftChange, onInvalidChange, t }: MCPRuntimeOptionsPanelProps) {
  const textareaId = useId();
  const draftText = mcpRuntimeOptionsText(draft.runtime_options);
  const [text, setText] = useState(draftText);
  const [error, setError] = useState("");

  useEffect(() => {
    setText(draftText);
    setError("");
    onInvalidChange?.(false);
  }, [draftText, onInvalidChange]);

  function commitText(nextText: string) {
    setText(nextText);
    const parsed = parseMCPRuntimeOptionsText(nextText);
    if (!parsed.ok) {
      setError(errorMessageForKey(parsed.error, t));
      onInvalidChange?.(true);
      return;
    }
    setError("");
    onInvalidChange?.(false);
    onDraftChange({
      ...draft,
      runtime_options: setMCPRuntimeOptions(draft.runtime_options, parsed.value),
    });
  }

  function fillExample() {
    const example = cloneMCPExample();
    setError("");
    onInvalidChange?.(false);
    setText(JSON.stringify(example, null, 2));
    onDraftChange({
      ...draft,
      runtime_options: setMCPRuntimeOptions(draft.runtime_options, example),
    });
  }

  function clearMCPConfig() {
    setError("");
    onInvalidChange?.(false);
    setText("");
    onDraftChange({
      ...draft,
      runtime_options: setMCPRuntimeOptions(draft.runtime_options, null),
    });
  }

  return (
    <div className="field span-2 mcp-runtime-options-panel">
      <div className="mcp-runtime-options-header">
        <label htmlFor={textareaId}>{t("profileMCPServers")}</label>
        <div className="mcp-runtime-options-actions">
          <Button variant="secondaryGray" size="sm" onClick={fillExample}>
            {t("profileMCPServersUseExample")}
          </Button>
          <Button variant="secondaryGray" size="sm" onClick={clearMCPConfig}>
            {t("profileMCPServersClear")}
          </Button>
        </div>
      </div>
      <textarea
        id={textareaId}
        className="compact-textarea mcp-runtime-options-textarea"
        value={text}
        aria-invalid={error ? "true" : undefined}
        aria-describedby={`${textareaId}-hint`}
        placeholder={t("profileMCPServersPlaceholder")}
        spellCheck={false}
        onInput={(event) => commitText(event.currentTarget.value)}
      />
      <span id={`${textareaId}-hint`} className={`field-hint${error ? " error" : ""}`.trim()}>
        {error || t("profileMCPServersHint")}
      </span>
    </div>
  );
}
