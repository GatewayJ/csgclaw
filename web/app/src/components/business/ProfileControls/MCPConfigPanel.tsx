import { useEffect, useId, useState } from "react";
import {
  MCP_CONFIG_EXAMPLE,
  mcpConfigText,
  parseMCPConfigText,
  setMCPConfig,
  type AgentDraft,
  type JSONRecord,
} from "@/models/agents";
import type { TranslateFn } from "@/models/conversations";
import { Button } from "@/components/ui/Button/Button";

export type MCPConfigPanelProps = {
  draft: AgentDraft;
  onDraftChange: (draft: AgentDraft) => void;
  onInvalidChange?: (invalid: boolean) => void;
  t: TranslateFn;
};

function cloneMCPExample(): JSONRecord {
  return JSON.parse(JSON.stringify(MCP_CONFIG_EXAMPLE)) as JSONRecord;
}

function errorMessageForKey(key: "invalid_json" | "object_required", t: TranslateFn): string {
  return key === "invalid_json" ? t("profileMCPServersInvalidJSON") : t("profileMCPServersObjectRequired");
}

export function MCPConfigPanel({ draft, onDraftChange, onInvalidChange, t }: MCPConfigPanelProps) {
  const textareaId = useId();
  const draftText = mcpConfigText(draft.mcp_config);
  const [text, setText] = useState(draftText);
  const [error, setError] = useState("");

  useEffect(() => {
    setText(draftText);
    setError("");
    onInvalidChange?.(false);
  }, [draftText, onInvalidChange]);

  function commitText(nextText: string) {
    setText(nextText);
    const parsed = parseMCPConfigText(nextText);
    if (!parsed.ok) {
      setError(errorMessageForKey(parsed.error, t));
      onInvalidChange?.(true);
      return;
    }
    setError("");
    onInvalidChange?.(false);
    onDraftChange({
      ...draft,
      mcp_config: setMCPConfig(parsed.value),
    });
  }

  function fillExample() {
    const example = cloneMCPExample();
    setError("");
    onInvalidChange?.(false);
    setText(JSON.stringify(example, null, 2));
    onDraftChange({
      ...draft,
      mcp_config: setMCPConfig(example),
    });
  }

  function clearMCPConfig() {
    setError("");
    onInvalidChange?.(false);
    setText("");
    onDraftChange({
      ...draft,
      mcp_config: setMCPConfig(null),
    });
  }

  return (
    <div className="field span-2 mcp-config-panel">
      <div className="mcp-config-header">
        <label htmlFor={textareaId}>{t("profileMCPServers")}</label>
        <div className="mcp-config-actions">
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
        className="compact-textarea mcp-config-textarea"
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
