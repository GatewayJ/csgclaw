import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { fetchAgenticHubOfficialSkillsPage, fetchSkills } from "@/api/skills";
import { useWorkspaceHubSelection } from "@/hooks/workspace/useWorkspaceHubSelection";
import { useWorkspaceUiStore } from "@/hooks/workspace/workspaceUiStore";
import type { TranslateFn } from "@/models/conversations";

vi.mock("@/api/hub", async () => {
  const actual = await vi.importActual<typeof import("@/api/hub")>("@/api/hub");
  return {
    ...actual,
    fetchHubTemplate: vi.fn(),
    fetchHubTemplates: vi.fn(async () => []),
    fetchHubWorkspace: vi.fn(async () => ({ entries: [], kind: "dir", path: "" })),
    fetchHubWorkspaceFile: vi.fn(),
  };
});

vi.mock("@/api/skills", async () => {
  const actual = await vi.importActual<typeof import("@/api/skills")>("@/api/skills");
  return {
    ...actual,
    fetchAgenticHubOfficialSkillsPage: vi.fn(),
    fetchSkillFile: vi.fn(),
    fetchSkills: vi.fn(),
    fetchSkillTree: vi.fn(),
  };
});

const t: TranslateFn = (key) => key;

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

describe("useWorkspaceHubSelection remote skills", () => {
  beforeEach(() => {
    vi.mocked(fetchAgenticHubOfficialSkillsPage).mockReset();
    vi.mocked(fetchSkills).mockReset();
    vi.mocked(fetchAgenticHubOfficialSkillsPage).mockResolvedValue({
      hasMore: false,
      items: [],
      nextPage: null,
      page: 1,
      per: 16,
      total: 0,
    });
    vi.mocked(fetchSkills).mockResolvedValue([]);
    useWorkspaceUiStore.setState({
      selectedHubResourceType: "template",
      selectedHubSkillName: "",
      selectedHubSkillPath: "",
      selectedHubTemplateId: "",
      selectedHubWorkspacePath: "",
    });
  });

  it("refreshes local skills when remote skills become visible or are refreshed", async () => {
    const { result } = renderHook(
      () =>
        useWorkspaceHubSelection({
          loaded: true,
          t,
          templates: [],
        }),
      { wrapper: createWrapper() },
    );

    await waitFor(() => expect(fetchSkills).toHaveBeenCalledTimes(1));
    vi.mocked(fetchAgenticHubOfficialSkillsPage).mockClear();
    vi.mocked(fetchSkills).mockClear();

    await act(async () => {
      result.current.setRemoteSkillsEnabled(true);
    });

    await waitFor(() => expect(fetchSkills).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(fetchAgenticHubOfficialSkillsPage).toHaveBeenCalled());
    vi.mocked(fetchAgenticHubOfficialSkillsPage).mockClear();
    vi.mocked(fetchSkills).mockClear();

    await act(async () => {
      await result.current.refetchRemoteSkills();
    });

    expect(fetchSkills).toHaveBeenCalledTimes(1);
    expect(fetchAgenticHubOfficialSkillsPage).toHaveBeenCalledTimes(1);
  });
});
