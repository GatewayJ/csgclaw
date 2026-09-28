import { act, renderHook } from "@testing-library/react";
import { fetchFeedbackStatus, submitFeedback } from "@/api/feedback";
import { useFeedback } from "@/pages/SettingsPage/useFeedback";

vi.mock("@/api/feedback", () => ({ fetchFeedbackStatus: vi.fn(), submitFeedback: vi.fn() }));
beforeEach(() => vi.clearAllMocks());

it("preserves content and checks an uncertain result before another POST", async () => {
  vi.mocked(submitFeedback).mockRejectedValueOnce(new Error("Network disconnected"));
  vi.mocked(fetchFeedbackStatus).mockResolvedValueOnce({ success: true, state: "succeeded", issue_id: 15 });
  const { result } = renderHook(() => useFeedback((key) => key));
  act(() => result.current.changeDescription("A problem"));
  await act(async () => {
    await result.current.submit();
  });
  expect(result.current.description).toBe("A problem");
  expect(result.current.locked).toBe(true);
  act(() => result.current.changeDescription("Another problem"));
  expect(result.current.description).toBe("A problem");
  await act(async () => {
    await result.current.submit();
  });
  expect(submitFeedback).toHaveBeenCalledOnce();
  expect(fetchFeedbackStatus).toHaveBeenCalledWith(vi.mocked(submitFeedback).mock.calls[0][0]);
  expect(result.current.success).toBe(true);
  expect(result.current.description).toBe("");
});

it("retries missing submissions with the same ID and content", async () => {
  vi.mocked(submitFeedback)
    .mockRejectedValueOnce(new Error("Network disconnected"))
    .mockResolvedValueOnce({ success: true, state: "succeeded" });
  vi.mocked(fetchFeedbackStatus).mockResolvedValueOnce({ success: false, state: "not_found" });
  const { result } = renderHook(() => useFeedback((key) => key));
  act(() => result.current.changeDescription("A problem"));
  await act(async () => {
    await result.current.submit();
  });
  await act(async () => {
    await result.current.submit();
  });
  await act(async () => {
    await result.current.submit();
  });
  expect(vi.mocked(submitFeedback).mock.calls[1]).toEqual(vi.mocked(submitFeedback).mock.calls[0]);
});

it("prevents simultaneous submits", async () => {
  let resolve: (value: { success: boolean; state: "succeeded" }) => void = () => {};
  vi.mocked(submitFeedback).mockReturnValue(
    new Promise((done) => {
      resolve = done;
    }),
  );
  const { result } = renderHook(() => useFeedback((key) => key));
  act(() => result.current.changeDescription("A problem"));
  let first: Promise<boolean>;
  act(() => {
    first = result.current.submit();
  });
  await act(async () => {
    await result.current.submit();
  });
  expect(submitFeedback).toHaveBeenCalledOnce();
  await act(async () => {
    resolve({ success: true, state: "succeeded" });
    await first;
  });
});

it("clears form state when the signed-in account or site changes", () => {
  const { result, rerender } = renderHook(({ scope }) => useFeedback((key) => key, scope), {
    initialProps: { scope: "user:site-a" },
  });
  act(() => result.current.changeDescription("Private feedback"));
  rerender({ scope: "user:site-b" });
  expect(result.current.description).toBe("");
  expect(result.current.locked).toBe(false);
});
