import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SidebarUserButton } from "@/pages/WorkspacePage/components/WorkspaceSidebar/SidebarUserButton";
import type { UpgradeStatus } from "@/models/upgradeStatus";

const labels: Record<string, string> = {
  appearanceSettings: "Appearance",
  languageSwitcher: "Language",
  settings: "Settings",
  themeDark: "Dark",
  themeLight: "Light",
  themeSwitcher: "Theme",
  upgradeAction: "Update & Restart",
  upgradeCurrentVersion: "Current version",
  upgradeLatestVersion: "Latest version",
  upgradeNoLatest: "Unknown",
  upgradeStatus: "Status",
  upgradeUpToDate: "Up to date",
  versionInfo: "Version",
  versionSettings: "Version and updates",
};

function t(key: string): string {
  return labels[key] ?? key;
}

const updateAvailableStatus: UpgradeStatus = {
  checking: false,
  current_version: "v0.3.0",
  last_checked_at: "",
  last_error: "",
  latest_version: "v0.3.1",
  manual_restart_required: false,
  update_available: true,
  upgrading: false,
};

describe("SidebarUserButton", () => {
  it("keeps the current version visible when upgrade controls are hidden", async () => {
    const user = userEvent.setup();
    const onOpenUpgrade = vi.fn();

    render(
      <SidebarUserButton
        appVersion="v0.3.0"
        showUpgradeControls={false}
        locale="en"
        onOpenUpgrade={onOpenUpgrade}
        onLocaleChange={() => {}}
        onThemeChange={() => {}}
        t={t}
        theme="light"
        upgradeStatus={updateAvailableStatus}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Settings" }));

    expect(screen.getByText("Current version")).toBeInTheDocument();
    expect(screen.getByText("v0.3.0")).toBeInTheDocument();
    expect(screen.getByText("Version")).toBeInTheDocument();
    expect(screen.queryByText("Version and updates")).not.toBeInTheDocument();
    expect(screen.queryByText("Latest version")).not.toBeInTheDocument();
    expect(screen.queryByText("Status")).not.toBeInTheDocument();
    expect(screen.queryByText("Update & Restart")).not.toBeInTheDocument();
    expect(onOpenUpgrade).not.toHaveBeenCalled();
  });
});
