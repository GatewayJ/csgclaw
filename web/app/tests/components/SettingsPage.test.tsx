import { fireEvent, render, screen } from "@testing-library/react";
import { WorkspaceControllerProvider } from "@/hooks/workspace";
import type { WorkspaceController } from "@/hooks/workspace";
import { emptyAuthStatus } from "@/models/auth";
import type { TranslateFn } from "@/models/conversations";
import { SettingsPage } from "@/pages/SettingsPage/SettingsPage";

const labels: Record<string, string> = {
  settings: "Settings",
  settingsAccountLogin: "Log in",
  settingsAppearanceDescription: "Appearance settings.",
  settingsCommunityAccount: "Community account",
  settingsCommunityAccountDescription: "Manage your community account.",
  settingsCurrentVersion: "Current version",
  settingsEnvironmentDescription: "Choose a site.",
  settingsFeedbackDescription: "Send feedback.",
  settingsPageSubtitle: "Manage product settings.",
  settingsParametersDescription: "Configure parameters.",
  settingsVersionDescription: "View the current version and update status.",
  upgradeAction: "Update & Restart",
};

const t: TranslateFn = (key) => labels[key] ?? key;

describe("SettingsPage", () => {
  it("opens the upgrade flow when an update is available", () => {
    const onOpenUpgrade = vi.fn();
    const controller = {
      ready: true,
      sidebarProps: {
        appVersion: "0.0.101",
        authBusy: false,
        authError: "",
        authPending: false,
        authStatus: emptyAuthStatus(),
        locale: "en",
        onLocaleChange: vi.fn(),
        onLogin: vi.fn(),
        onLogout: vi.fn(),
        onOpenConfigSettings: vi.fn(),
        onOpenUpgrade,
        onThemeChange: vi.fn(),
        showUpgradeControls: true,
        t,
        theme: "light",
        upgradeBusy: false,
        upgradePhase: "idle",
        upgradeStatus: {
          auto_upgrade_supported: true,
          auto_upgrade_unsupported_reason: "",
          checking: false,
          current_version: "0.0.101",
          last_checked_at: "",
          last_error: "",
          last_error_kind: "",
          last_error_log_path: "",
          latest_version: "v0.3.18",
          manual_restart_required: false,
          update_available: true,
          upgrading: false,
        },
      },
    } as unknown as WorkspaceController;

    render(
      <WorkspaceControllerProvider controller={controller}>
        <SettingsPage />
      </WorkspaceControllerProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Update & Restart" }));

    expect(onOpenUpgrade).toHaveBeenCalledTimes(1);
  });
});
