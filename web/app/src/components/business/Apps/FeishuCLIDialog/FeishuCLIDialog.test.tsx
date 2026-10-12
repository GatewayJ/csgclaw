import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createTranslator } from "@/shared/i18n";
import type { AppInstallation } from "@/api/apps";
import { fetchAppResource, cancelCLIOperation, startCLIOperation } from "@/api/apps";
import { FeishuCLIDialog } from "./FeishuCLIDialog";

vi.mock("@/api/apps", () => ({
  fetchAppResource: vi.fn(),
  ensureFeishuResource: vi.fn(),
  startCLIOperation: vi.fn(),
  cancelCLIOperation: vi.fn(),
}));
const t = createTranslator("en");
const resource: AppInstallation = {
  installation_id: "global-cli",
  agent_id: "",
  app_id: "feishu",
  name: "Feishu",
  kind: "cli",
  revision: 4,
  enabled: true,
  disconnected: false,
  status: "connecting",
  config: {},
  credentials_set: {},
  tools: [],
  created_at: "",
  updated_at: "",
  operation: {
    operation_id: "connect-1",
    session_id: "session-1",
    resource_id: "global-cli",
    resource_revision: 4,
    action: "connect",
    stage: "user_authorization",
    status: "running",
    authorization_url: "https://open.feishu.cn/device",
  },
};
afterEach(() => vi.resetAllMocks());
describe("Feishu CLI authorization", () => {
  it("disconnects both identities through one action", async () => {
    const connected: AppInstallation = {
      ...resource,
      status: "connected",
      operation: undefined,
      auth_status: {
        appId: "cli_test",
        defaultAs: "auto",
        identities: {
          bot: { status: "ready", available: true, verified: true },
          user: { status: "ready", available: true, verified: true },
        },
      },
    };
    vi.mocked(fetchAppResource).mockResolvedValue(connected);
    vi.mocked(startCLIOperation).mockResolvedValue({ ...resource.operation!, action: "disconnect" });
    render(<FeishuCLIDialog existing={connected} t={t} onClose={vi.fn()} onChanged={vi.fn()} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Disconnect" }));
    expect(startCLIOperation).toHaveBeenCalledWith(connected, "disconnect", expect.any(String));
  });
  it.each([null, []])("renders completed migration with workers %j", async (workers) => {
    const completed: AppInstallation = {
      ...resource,
      status: "connected",
      operation: undefined,
      migration: { status: "completed", workers },
    };
    vi.mocked(fetchAppResource).mockResolvedValue(completed);
    render(<FeishuCLIDialog existing={completed} t={t} onClose={vi.fn()} onChanged={vi.fn()} />);
    expect(await screen.findByRole("button", { name: "Close" })).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  it("restores the active authorization and keeps it running when the dialog closes", async () => {
    vi.mocked(fetchAppResource).mockResolvedValue(resource);
    const close = vi.fn();
    render(<FeishuCLIDialog existing={resource} t={t} onClose={close} onChanged={vi.fn()} />);
    expect(await screen.findByRole("link", { name: "Open Feishu authorization" })).toHaveAttribute(
      "href",
      "https://open.feishu.cn/device",
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Close" }));
    expect(close).toHaveBeenCalledOnce();
    expect(cancelCLIOperation).not.toHaveBeenCalled();
    expect(startCLIOperation).not.toHaveBeenCalled();
  });
  it("cancels the persisted session explicitly", async () => {
    vi.mocked(fetchAppResource).mockResolvedValue(resource);
    vi.mocked(cancelCLIOperation).mockResolvedValue({
      ...resource.operation!,
      status: "cancelled",
      authorization_url: undefined,
    });
    render(<FeishuCLIDialog existing={resource} t={t} onClose={vi.fn()} onChanged={vi.fn()} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(cancelCLIOperation).toHaveBeenCalledWith("global-cli", "session-1"));
    expect(screen.queryByRole("link", { name: "Open Feishu authorization" })).not.toBeInTheDocument();
  });
  it("shows migration failures and offers reconnection", async () => {
    const failed = {
      ...resource,
      status: "error" as const,
      last_error_code: "migration_failed",
      operation: undefined,
      migration: {
        status: "waiting_for_legacy_callers",
        workers: [
          { agent_id: "worker", phase: "original", error_code: "migration_failed", verification_pending: false },
        ],
      },
    };
    vi.mocked(fetchAppResource).mockResolvedValue(failed);
    render(<FeishuCLIDialog existing={failed} t={t} onClose={vi.fn()} onChanged={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Historical configuration migration needs repair.");
    expect(screen.getByRole("button", { name: "Connect Feishu" })).toBeEnabled();
  });
});
