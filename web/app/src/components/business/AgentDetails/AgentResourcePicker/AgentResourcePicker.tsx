import { useState } from "react";
import { BookOpen, ExternalLink, Plus, Upload } from "lucide-react";
import {
  Button,
  Checkbox,
  DialogBody,
  DialogCloseButton,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
  LinkButton,
  SegmentedControl,
  TextInput,
} from "@/components/ui";
import { SkillUploadDialog } from "@/components/business/SkillUploadDialog";
import { MCPServerCreateDialog } from "@/components/business/HubDetailPane";
import { useAgentResourcePicker, type AgentResourcePickerKind } from "@/hooks/workspace/useAgentResourcePicker";
import { knowledgeBaseMCPServer } from "@/models/knowledgeBases";
import { isRemoteMCPInstalled, mcpManagedKnowledgeBaseSource, mcpServerDisplayName } from "@/models/mcp";
import { remoteSkillInstallName } from "@/models/skillhub";
import type { TranslateFn } from "@/models/conversations";
import { localizeAPIError } from "@/shared/i18n";
import { errorMessage } from "@/api/client";
import styles from "./AgentResourcePicker.module.css";

export function AgentResourcePicker({
  agentID,
  kind,
  authenticated,
  error,
  onAdd,
  onClose,
  onLogin,
  portalContainer,
  t,
}: {
  agentID: string;
  kind: AgentResourcePickerKind;
  authenticated: boolean;
  error?: string;
  onAdd: (names: string[]) => Promise<boolean> | boolean;
  onClose: () => void;
  onLogin?: () => boolean;
  portalContainer?: HTMLElement | null;
  t: TranslateFn;
}) {
  const picker = useAgentResourcePicker({ agentID, kind, authenticated, onAdd, t });
  const [selected, setSelected] = useState<string[]>([]);
  const [uploading, setUploading] = useState(false);
  const [creating, setCreating] = useState(false);
  const title = t(kind === "skill" ? "agentSkillAdd" : kind === "mcp" ? "agentMCPAdd" : "agentKnowledgeAdd");
  const busy = Boolean(picker.busy);
  const marketQuery = kind === "skill" ? picker.skillMarket : kind === "mcp" ? picker.mcpMarket : picker.knowledge;
  const loadError = picker.libraryError || ((picker.market || kind === "knowledge") && marketQuery.error);
  const failure = picker.error || error || (loadError ? localizeAPIError(loadError, t, errorMessage(loadError)) : "");
  const match = (name: string, description = "") =>
    `${name} ${description}`.toLowerCase().includes(picker.search.trim().toLowerCase());
  const library =
    kind === "skill"
      ? picker.skills
          .filter((skill) => !picker.installedNames.has(skill.name))
          .map((skill) => ({ name: skill.name, label: skill.name, description: skill.description }))
      : picker.globalServers
          .filter((server) => Boolean(mcpManagedKnowledgeBaseSource(server.config)) === (kind === "knowledge"))
          .map((server) => ({
            name: server.name,
            label: mcpServerDisplayName(server),
            description: server.description,
          }));
  const visibleLibrary = library.filter((item) => match(item.label, item.description));
  async function finish(operation: Promise<boolean>) {
    const added = await operation;
    if (added) onClose();
    return added;
  }
  function select(name: string, checked: boolean) {
    setSelected((current) => (checked ? [...current, name] : current.filter((item) => item !== name)));
  }
  const remoteRows =
    kind === "skill"
      ? (picker.skillMarket.data?.pages.flatMap((page) => page.items) ?? []).map((skill) => ({
          id: skill.remotePath || skill.name,
          name: skill.name,
          description: skill.description,
          installed: picker.installedNames.has(remoteSkillInstallName(skill)),
          unavailable: false,
          add: () => picker.installSkill(skill),
        }))
      : kind === "mcp"
        ? (picker.mcpMarket.data?.pages.flatMap((page) => page.items) ?? []).map((server) => ({
            id: server.id,
            name: server.name,
            description: server.description,
            installed: picker.globalServers.some(
              (item) => picker.installedNames.has(item.name) && isRemoteMCPInstalled([item], server),
            ),
            unavailable: false,
            add: () => picker.installMCP(server),
          }))
        : picker.knowledgeItems
            .filter((item) => !knowledgeBaseMCPServer(item, picker.globalServers))
            .map((item) => ({
              id: item.id,
              name: item.name,
              description: item.unavailableReason || item.description,
              installed: false,
              unavailable: item.availability !== "available",
              add: () => picker.connectKnowledge(item),
            }));
  const marketLabel = t(kind === "skill" ? "profileSkillMarket" : "profileMCPMarket");
  return (
    <>
      <DialogRoot
        open
        onOpenChange={(open) => {
          if (!open && !busy) onClose();
        }}
      >
        <DialogContent className={styles.dialog} portalContainer={portalContainer}>
          <DialogHeader>
            <div>
              <DialogTitle>{title}</DialogTitle>
              <DialogDescription>
                {t(
                  kind === "skill"
                    ? "profileSkillPickerHint"
                    : kind === "mcp"
                      ? "profileMCPPickerHint"
                      : "profileKnowledgePickerHint",
                )}
              </DialogDescription>
            </div>
            <DialogCloseButton label={t("close")} disabled={busy} size="sm" variant="tertiaryGray" />
          </DialogHeader>
          <DialogBody className={styles.body}>
            {kind !== "knowledge" ? (
              <SegmentedControl
                label={title}
                value={picker.market ? "market" : "library"}
                onChange={(value) => {
                  picker.setMarket(value === "market");
                  picker.setSearch("");
                }}
                options={[
                  { value: "library", label: t("profileResourceLibrary") },
                  { value: "market", label: marketLabel },
                ]}
              />
            ) : null}
            <TextInput
              type="search"
              value={picker.search}
              aria-label={t("profileResourceSearch")}
              placeholder={t("profileResourceSearch")}
              onChange={(event) => picker.setSearch(event.currentTarget.value)}
            />
            {failure ? (
              <div className="form-error" role="alert">
                {failure}
                {loadError ? (
                  <Button
                    size="sm"
                    onClick={() => {
                      void picker.retryLibrary();
                      void marketQuery.refetch();
                    }}
                  >
                    {t("retry")}
                  </Button>
                ) : null}
              </div>
            ) : null}
            <div className={styles.list}>
              {!picker.market ? (
                <>
                  {picker.libraryLoading ? <p role="status">{t("loading")}</p> : null}
                  {visibleLibrary.map((item) => {
                    const installed = picker.installedNames.has(item.name);
                    return (
                      <label className={styles.row} key={item.name}>
                        <Checkbox
                          checked={installed || selected.includes(item.name)}
                          disabled={busy || installed || Boolean(picker.libraryError)}
                          onCheckedChange={(checked) => select(item.name, checked === true)}
                          aria-label={item.label}
                        />
                        <span className={styles.copy}>
                          <strong>{item.label}</strong>
                          <span>{item.description}</span>
                        </span>
                        {installed ? <span className={styles.status}>{t("profileResourceAdded")}</span> : null}
                      </label>
                    );
                  })}
                  {!visibleLibrary.length && !picker.libraryLoading && !picker.libraryError && kind !== "knowledge" ? (
                    <p>{t("profileResourceLibraryEmpty")}</p>
                  ) : null}
                </>
              ) : null}
              {picker.market || kind === "knowledge" ? (
                <>
                  {!authenticated ? (
                    <div className={styles.login}>
                      <span>
                        {t(
                          kind === "skill"
                            ? "iaSkillsMarketLoginRequired"
                            : kind === "mcp"
                              ? "iaMCPMarketLoginRequired"
                              : "resourcesKnowledgeBasesLoginRequired",
                        )}
                      </span>
                      <Button onClick={() => onLogin?.()}>{t("resourcesKnowledgeBasesLogin")}</Button>
                    </div>
                  ) : (
                    <>
                      {marketQuery.isFetching && !marketQuery.isFetchingNextPage ? (
                        <p role="status">{t("loading")}</p>
                      ) : null}
                      {remoteRows.map((item) => (
                        <div key={item.id} className={styles.row}>
                          {kind === "knowledge" ? <BookOpen size={20} /> : null}
                          <span className={styles.copy}>
                            <strong>{item.name}</strong>
                            <span>{item.description}</span>
                          </span>
                          <Button
                            size="sm"
                            variant="secondaryGray"
                            loading={picker.busy === item.id}
                            disabled={
                              busy ||
                              item.installed ||
                              item.unavailable ||
                              picker.libraryLoading ||
                              Boolean(picker.libraryError)
                            }
                            onClick={() => void finish(item.add())}
                          >
                            {t(
                              item.installed
                                ? "profileResourceAdded"
                                : item.unavailable
                                  ? "resourcesKnowledgeBaseUnavailable"
                                  : kind === "knowledge"
                                    ? "profileKnowledgeConnectEnable"
                                    : "profileResourceInstallEnable",
                            )}
                          </Button>
                        </div>
                      ))}
                      {!marketQuery.isFetching &&
                      !marketQuery.error &&
                      !remoteRows.length &&
                      (picker.market || !visibleLibrary.length) ? (
                        <p>{t("workspaceSearchNoResults")}</p>
                      ) : null}
                      {marketQuery.hasNextPage ? (
                        <Button
                          size="sm"
                          disabled={marketQuery.isFetching}
                          onClick={() => void marketQuery.fetchNextPage()}
                        >
                          {t("resourceLoadMore")}
                        </Button>
                      ) : null}
                    </>
                  )}
                </>
              ) : null}
            </div>
          </DialogBody>
          <DialogFooter className={styles.footer}>
            <div className={styles.links}>
              {kind === "skill" ? (
                <Button size="sm" disabled={busy} onClick={() => setUploading(true)}>
                  <Upload size={15} />
                  {t("resourcesSkillUpload")}
                </Button>
              ) : kind === "mcp" ? (
                <Button size="sm" disabled={busy} onClick={() => setCreating(true)}>
                  <Plus size={15} />
                  {t("resourcesMCPCreateTitle")}
                </Button>
              ) : null}
              <LinkButton
                size="sm"
                href={kind === "skill" ? "#/skills" : kind === "mcp" ? "#/connectors" : "#/knowledge-bases"}
              >
                <ExternalLink size={15} />
                {t(
                  kind === "skill"
                    ? "profileManageSkills"
                    : kind === "mcp"
                      ? "profileManageMCP"
                      : "profileManageKnowledge",
                )}
              </LinkButton>
            </div>
            <div className={styles.links}>
              <Button size="sm" disabled={busy} onClick={onClose}>
                {t("cancel")}
              </Button>
              {!picker.market ? (
                <Button
                  size="sm"
                  variant="primary"
                  loading={picker.busy === "library"}
                  disabled={busy || !selected.length || Boolean(picker.libraryError)}
                  onClick={() => void finish(picker.add(selected))}
                >
                  {title}
                </Button>
              ) : null}
            </div>
          </DialogFooter>
        </DialogContent>
      </DialogRoot>
      <SkillUploadDialog
        open={uploading}
        zipOnly
        onOpenChange={setUploading}
        busy={busy}
        error={failure}
        installedSkills={picker.skills}
        onSubmit={(file) => finish(picker.upload(file))}
        remoteInstallBusy=""
        remoteInstallError=""
        remoteSkills={[]}
        remoteSkillsError=""
        remoteSkillsHasMore={false}
        remoteSkillsLoading={false}
        remoteSkillsLoadingMore={false}
        remoteSkillsSearch=""
        t={t}
      />
      <MCPServerCreateDialog
        open={creating}
        manualOnly
        onOpenChange={setCreating}
        busy={busy}
        createError={failure}
        initialDocument=""
        onCreate={(payload) => finish(picker.createMCP(payload))}
        remote={{
          error: "",
          hasMore: false,
          installBusy: "",
          installedServers: [],
          items: [],
          loading: false,
          loadingMore: false,
          search: "",
        }}
        t={t}
      />
    </>
  );
}
