import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { createQueryWrapper } from "../../../../tests/helpers/queryClient";
import { ConversationResourcePicker } from "./ConversationResourcePicker";
import { fetchConversationResourceBatch } from "@/api/agentResources";
import type { AgentResourceSummary } from "@/models/conversationResources";

vi.mock("@/api/agentResources", () => ({ fetchConversationResourceBatch: vi.fn() }));
const item = (id: string, available = true): AgentResourceSummary => ({
  source: "app",
  id,
  name: "GitLab",
  type: "connector",
  enabled: available,
  available,
});

it("服务器搜索与分页保留同名实例身份，并禁止选择停用资源", async () => {
  const fetch = vi.mocked(fetchConversationResourceBatch);
  fetch.mockImplementation(async (_agent, _type, cursor, request) => ({
    items:
      request?.search === "special"
        ? [item("searched")]
        : cursor
          ? [item("second")]
          : [item("first"), item("disabled", false)],
    total: 3,
    list_revision: "revision",
    has_more: !cursor && !request?.search,
    next_cursor: !cursor && !request?.search ? "page-2" : undefined,
  }));
  const pick = vi.fn();
  const { wrapper } = createQueryWrapper();
  render(
    <MemoryRouter>
      <ConversationResourcePicker agentID="agent" kind="connector" t={(key) => key} onClose={() => {}} onPick={pick} />
    </MemoryRouter>,
    { wrapper },
  );
  const user = userEvent.setup();
  await screen.findByText("app:first");
  const checks = screen.getAllByRole("checkbox");
  expect(checks[1]).toBeDisabled();
  await user.click(checks[0]);
  await user.click(screen.getByRole("button", { name: "taskEntryLoadMore" }));
  await screen.findByText("app:second");
  await user.click(screen.getAllByRole("checkbox")[2]);
  await user.type(screen.getByRole("textbox", { name: "search" }), "special");
  await screen.findByText("app:searched");
  await user.click(screen.getByRole("button", { name: "addAttachmentResource" }));
  expect(pick.mock.calls[0][0].map((ref: AgentResourceSummary) => ref.id)).toEqual(["first", "second"]);
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith("agent", "connector", "", { search: "special" }, expect.any(AbortSignal)),
  );
});
