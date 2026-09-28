import { act, renderHook, waitFor } from "@testing-library/react";
import { fetchAgentSkillPage, fetchAgentMCPPage } from "@/api/agentResources";
import { useAgentResourcePages } from "@/hooks/workspace/useAgentResourcePages";
import { workspaceQueryKeys } from "@/hooks/workspace/workspaceQueries";
import { createQueryWrapper } from "../helpers/queryClient";

vi.mock("@/api/agentResources", () => ({ fetchAgentSkillPage: vi.fn(), fetchAgentMCPPage: vi.fn() }));
it("preserves the first visible position on resize, clamps deleted pages, and isolates agents", async () => {
  let total = 25;
  vi.mocked(fetchAgentSkillPage).mockImplementation(async (id, request) => ({
    items: Array.from({ length: total }, (_, index) => ({ name: `${id}-${index}` })).slice(
      (request.page - 1) * request.per,
      request.page * request.per,
    ),
    total,
    page: request.page,
    per: request.per,
    list_revision: "test",
    has_more: request.page * request.per < total,
  }));
  vi.mocked(fetchAgentMCPPage).mockResolvedValue({
    items: [],
    page: 1,
    per: 12,
    total: 0,
    list_revision: "mcp",
    has_more: false,
  });
  const query = createQueryWrapper();
  const { result, rerender } = renderHook(({ id }) => useAgentResourcePages(id), {
    initialProps: { id: "alpha" },
    wrapper: query.wrapper,
  });
  await waitFor(() => expect(result.current.skills.query.isSuccess).toBe(true));
  act(() => result.current.skills.pagination.onPageChange(2));
  await waitFor(() => expect(result.current.skills.query.data?.items[0].name).toBe("alpha-12"));
  act(() => result.current.skills.pagination.onCapacityChange(6));
  await waitFor(() => expect(result.current.skills.pagination.page).toBe(3));
  await waitFor(() => expect(result.current.skills.query.isFetching).toBe(false));
  expect(result.current.skills.query.data?.items[0].name).toBe("alpha-12");
  act(() => result.current.skills.pagination.onPageChange(5));
  await waitFor(() => expect(result.current.skills.query.data?.items[0].name).toBe("alpha-24"));
  total = 24;
  await act(async () => {
    await query.client.invalidateQueries({ queryKey: workspaceQueryKeys.agentSkills("alpha") });
  });
  await waitFor(() => expect(result.current.skills.pagination.page).toBe(4));
  await waitFor(() => expect(result.current.skills.query.data?.items[0].name).toBe("alpha-18"));
  rerender({ id: "beta" });
  expect(result.current.skills.pagination.page).toBe(1);
  await waitFor(() => expect(result.current.skills.query.data?.items[0].name).toBe("beta-0"));
});
