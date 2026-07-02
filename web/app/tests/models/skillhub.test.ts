import { describe, expect, it } from "vitest";
import {
  hasInstalledRemoteSkill,
  isReadonlySkill,
  skillNameFromRemotePath,
  skillSourceBadgeName,
} from "@/models/skillhub";

describe("skill hub helpers", () => {
  it("treats official and system skills as readonly", () => {
    expect(isReadonlySkill({ name: "remote", source: "official" })).toBe(true);
    expect(isReadonlySkill({ name: "mine", source: "personal" })).toBe(true);
    expect(isReadonlySkill({ name: "system", source: "system" })).toBe(true);
    expect(isReadonlySkill({ name: "builtin", readonly: true })).toBe(true);
    expect(isReadonlySkill({ name: "local" })).toBe(false);
  });

  it("uses source badges for builtin, official, personal, and local skills", () => {
    expect(skillSourceBadgeName({ name: "remote", source: "official" })).toBe("official");
    expect(skillSourceBadgeName({ name: "mine", source: "personal" })).toBe("personal");
    expect(skillSourceBadgeName({ name: "builtin", source: "builtin" })).toBe("builtin");
    expect(skillSourceBadgeName({ name: "system", source: "system" })).toBe("builtin");
    expect(skillSourceBadgeName({ name: "readonly", readonly: true })).toBe("builtin");
    expect(skillSourceBadgeName({ name: "local" })).toBe("local");
    expect(skillSourceBadgeName(null)).toBe("");
  });

  it("matches installed remote skills by the remote path skill name", () => {
    const installedSkills = [{ name: "agent-builder" }, { name: "local-only" }];

    expect(
      hasInstalledRemoteSkill(installedSkills, {
        name: "Agent Builder",
        remotePath: "AIWizards/agent-builder",
      }),
    ).toBe(true);
    expect(
      hasInstalledRemoteSkill(installedSkills, {
        name: "missing",
        remotePath: "AIWizards/missing",
      }),
    ).toBe(false);
  });

  it("extracts the installed skill name from remote paths", () => {
    expect(skillNameFromRemotePath("AIWizards/agent-builder")).toBe("agent-builder");
    expect(skillNameFromRemotePath(" owner / nested / skill ")).toBe("skill");
    expect(skillNameFromRemotePath("")).toBe("");
  });
});
