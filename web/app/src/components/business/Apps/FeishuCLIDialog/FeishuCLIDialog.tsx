import { useEffect, useRef, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import {
  ensureFeishuResource,
  fetchAppResource,
  startCLIOperation,
  cancelCLIOperation,
  type AppInstallation,
  type CLIAction,
} from "@/api/apps";
import {
  Button,
  DialogRoot,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogBody,
  DialogFooter,
  DialogCloseButton,
} from "@/components/ui";
import type { TranslateFn } from "@/models/conversations";
import { localizeAPIError } from "@/shared/i18n";
import { errorMessage } from "@/api/client";
import { appStatus } from "../appForm";
import styles from "../AgentAppsPanel.module.css";

type Props = {
  existing: AppInstallation | null;
  t: TranslateFn;
  onClose: () => void;
  onChanged: () => Promise<unknown>;
};

export function FeishuCLIDialog({ existing, t, onClose, onChanged }: Props) {
  const [resource, setResource] = useState(existing);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const retry = useRef<{ action: CLIAction; id: string; revision: number | undefined } | null>(null);
  const resourceID = resource?.installation_id;
  const migrationWorkers = resource?.migration?.workers ?? [];
  useEffect(() => {
    if (!resourceID) return;
    const abort = new AbortController();
    let pending = false;
    const refresh = async () => {
      if (pending) return;
      pending = true;
      try {
        const value = await fetchAppResource(resourceID, abort.signal);
        if (!abort.signal.aborted) setResource(value);
      } catch (failure) {
        if (!abort.signal.aborted) setError(failure);
      } finally {
        pending = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 1500);
    return () => {
      abort.abort();
      window.clearInterval(timer);
    };
  }, [resourceID]);
  async function run(action: CLIAction) {
    setBusy(true);
    setError(null);
    try {
      const current = resource ?? (await ensureFeishuResource());
      setResource(current);
      if (retry.current?.action !== action || retry.current.revision !== current.revision) {
        retry.current = {
          action,
          id:
            typeof crypto.randomUUID === "function"
              ? crypto.randomUUID()
              : `cli-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`,
          revision: current.revision,
        };
      }
      const operation = await startCLIOperation(current, action, retry.current.id);
      retry.current = null;
      setResource({ ...current, operation });
      await onChanged();
    } catch (failure) {
      setError(failure);
    } finally {
      setBusy(false);
    }
  }
  async function cancel() {
    if (!resource?.operation) return;
    setBusy(true);
    try {
      const operation = await cancelCLIOperation(resource.installation_id, resource.operation.session_id);
      setResource({ ...resource, operation });
    } catch (failure) {
      setError(failure);
    } finally {
      setBusy(false);
    }
  }
  const operation = resource?.operation;
  const active = operation?.status === "running" || operation?.status === "pending";
  const ready = Boolean(
    resource?.auth_status &&
    !resource.disconnected &&
    Object.values(resource.auth_status.identities).some(
      (identity) => identity.available && identity.verified !== false,
    ),
  );
  const stages: Record<string, string> = {
    pending: "feishuCLIPreparing",
    checking_environment: "feishuCLIPreparing",
    downloading_cli: "feishuCLIInstalling",
    downloading_skills: "feishuCLIInstalling",
    installing_skills: "feishuCLIInstalling",
    verifying: "feishuCLIInstalling",
    verifying_installation: "feishuCLIInstalling",
    app_authorization: "feishuCLIAppAuthorization",
    user_authorization: "feishuCLIUserAuthorization",
    applying: "feishuCLIApplying",
    completed: "feishuCLICompleted",
  };
  const errorLabels: Record<string, string> = {
    authorization_expired: "feishuCLIExpired",
    authorization_denied: "feishuCLIDenied",
    permission_denied: "feishuCLIPermissionDenied",
    cleanup_pending: "feishuCLICleanupPending",
    migration_failed: "feishuCLIMigrationFailed",
  };
  return (
    <DialogRoot
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className={styles.settingsDialog}>
        <DialogHeader>
          <div>
            <DialogTitle>{t("appFeishuName")}</DialogTitle>
            <DialogDescription>{t("feishuCLIIntro")}</DialogDescription>
          </div>
          <DialogCloseButton label={t("close")} variant="tertiaryGray" size="sm" iconOnly />
        </DialogHeader>
        <DialogBody className={styles.settingsBody}>
          {error ? (
            <p role="alert" className="form-error">
              {localizeAPIError(error, t) || errorMessage(error)}
            </p>
          ) : null}
          {resource?.last_error_code ? (
            <p role="alert" className="form-error">
              {t(errorLabels[resource.last_error_code] ?? "feishuCLIRepairHint")}
            </p>
          ) : null}
          <p role="status" aria-live="polite">
            {active
              ? t(stages[operation?.stage ?? ""] ?? "feishuCLIPreparing")
              : resource
                ? appStatus(resource, t)
                : t("appStatusNeedsConfiguration")}
          </p>
          {operation?.authorization_url && active ? (
            <div className="flex flex-col items-start gap-3">
              <QRCodeSVG
                value={operation.authorization_url}
                size={168}
                marginSize={2}
                title={t("feishuCLIAuthorize")}
              />
              <a
                href={operation.authorization_url}
                target="_blank"
                rel="noopener noreferrer"
                className="btn btn-primary"
              >
                {t("feishuCLIAuthorize")}
              </a>
              <p className={styles.hint}>{t("feishuCLIWaitHint")}</p>
            </div>
          ) : null}
          {(["bot", "user"] as const).map((identity) => {
            const info = resource?.auth_status?.identities[identity];
            return (
              <div key={identity} className={styles.resourceDetails}>
                <strong>{t(identity === "bot" ? "feishuCLIBot" : "feishuCLIUser")}</strong>
                <p>
                  {info?.appName || info?.userName || resource?.auth_status?.appId || "—"} ·{" "}
                  {t(info?.available && info.verified !== false ? "feishuCLIAvailable" : "feishuCLIUnauthorized")}
                </p>
                {identity === "user" ? (
                  <>
                    <p className={styles.hint}>{t("feishuCLIRequestedScopes")}</p>
                    {resource?.requested_scopes ? <p className="break-words">{resource.requested_scopes}</p> : null}
                    {info?.scope ? (
                      <p className="break-words">
                        {t("feishuCLIGrantedScopes")}: {info.scope}
                      </p>
                    ) : null}
                  </>
                ) : null}
              </div>
            );
          })}
          {resource?.migration && migrationWorkers.length > 0 ? (
            <div>
              <p>
                {t(
                  resource.migration.status === "completed"
                    ? "feishuCLIMigrationCompleted"
                    : "feishuCLIMigrationPending",
                )}
              </p>
              <ul>
                {migrationWorkers.map((worker) => (
                  <li key={worker.agent_id}>
                    {worker.agent_id} ·{" "}
                    {t(
                      worker.error_code
                        ? "feishuCLIMigrationFailed"
                        : worker.verification_pending
                          ? "feishuCLIVerificationPending"
                          : worker.phase === "completed"
                            ? "feishuCLICompleted"
                            : "feishuCLIMigrationPending",
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {resource?.checked_at && !resource.checked_at.startsWith("0001") ? (
            <p className={styles.hint}>
              {t("feishuCLICheckedAt", { time: new Date(resource.checked_at).toLocaleString() })}
            </p>
          ) : null}
          {resource?.package_version ? (
            <p className={styles.hint}>{t("feishuCLIVersion", { version: resource.package_version })}</p>
          ) : null}
          <p className={styles.hint}>{t("appResourceUsers", { count: resource?.bindings?.length ?? 0 })}</p>
          <ul className={styles.bindingList}>
            {resource?.bindings?.map((binding) => (
              <li key={binding.installation_id}>
                <a
                  href={`#/agents/${encodeURIComponent(binding.agent_id)}?tab=connectors&connector=${encodeURIComponent(binding.installation_id)}`}
                >
                  {binding.agent_name || binding.agent_id}
                </a>
                <span>
                  {appStatus({ ...resource, enabled: binding.enabled, status: binding.status, disconnected: false }, t)}
                  {binding.runtime_status === "configured"
                    ? ` · ${t("feishuCLIConfiguredRuntime")}`
                    : binding.runtime_status === "loaded"
                      ? ` · ${t("feishuCLILoadedRuntime")}`
                      : ""}
                </span>
              </li>
            ))}
          </ul>
        </DialogBody>
        <DialogFooter>
          {active ? (
            <Button disabled={busy} onClick={() => void cancel()}>
              {t("cancel")}
            </Button>
          ) : (
            <>
              {ready ? (
                <>
                  {!resource?.auth_status?.identities.user.available ? (
                    <Button disabled={busy} onClick={() => void run("login")}>
                      {t("feishuCLILogin")}
                    </Button>
                  ) : null}
                  <Button disabled={busy} onClick={() => void run("disconnect")}>
                    {t("appDisconnect")}
                  </Button>
                </>
              ) : null}
              {!ready ? (
                <Button variant="primary" disabled={busy} onClick={() => void run("connect")}>
                  {t("feishuCLIConnect")}
                </Button>
              ) : null}
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </DialogRoot>
  );
}
