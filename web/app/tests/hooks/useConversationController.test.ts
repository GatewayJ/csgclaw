import { slashSkillCommandText, slashSkillQueryForDraft } from "@/hooks/workspace/useConversationController";

describe("useConversationController slash skill helpers", () => {
  it("keeps the skill picker open only while editing the command name", () => {
    expect(slashSkillQueryForDraft("/")).toBe("");
    expect(slashSkillQueryForDraft("  /sk")).toBe("sk");
    expect(slashSkillQueryForDraft("/skill-creator ")).toBeNull();
    expect(slashSkillQueryForDraft("/skill-creator make a skill")).toBeNull();
    expect(slashSkillQueryForDraft("hello /skill-creator")).toBeNull();
  });

  it("renders selected skills as canonical slash-command XML", () => {
    expect(slashSkillCommandText("skill-creator")).toBe(
      '<slash-command name="use-skill" arg="skill-creator"></slash-command> ',
    );
    expect(slashSkillCommandText('a&b"c<d>')).toBe(
      '<slash-command name="use-skill" arg="a&amp;b&quot;c&lt;d&gt;"></slash-command> ',
    );
  });
});
