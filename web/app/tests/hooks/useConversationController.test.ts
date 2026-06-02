import { slashSkillQueryForDraft } from "@/hooks/workspace/useConversationController";

describe("useConversationController slash skill helpers", () => {
  it("keeps the skill picker open only while editing the command name", () => {
    expect(slashSkillQueryForDraft("/")).toBe("");
    expect(slashSkillQueryForDraft("  /sk")).toBe("sk");
    expect(slashSkillQueryForDraft("/skill-creator ")).toBeNull();
    expect(slashSkillQueryForDraft("/skill-creator make a skill")).toBeNull();
    expect(slashSkillQueryForDraft("hello /skill-creator")).toBeNull();
  });
});
