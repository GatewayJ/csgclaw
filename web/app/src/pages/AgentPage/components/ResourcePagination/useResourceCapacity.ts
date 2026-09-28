import { useEffect, useRef } from "react";
import type { RefObject } from "react";
import type { ResourcePagination as PaginationState } from "@/hooks/workspace/useAgentResourcePages";

export function useResourceCapacity(pagination?: PaginationState, paused = false): RefObject<HTMLElement | null> {
  const enabled = Boolean(pagination);
  const ref = useRef<HTMLElement>(null);
  const onCapacityChange = useRef(pagination?.onCapacityChange);
  useEffect(() => {
    onCapacityChange.current = pagination?.onCapacityChange;
  });
  useEffect(() => {
    const element = ref.current;
    if (!element || !enabled || paused || typeof ResizeObserver === "undefined") return;
    const editor = element.closest<HTMLElement>(".agent-profile-scroll-region");
    if (!editor) return;
    let timer: ReturnType<typeof setTimeout>;
    function measure() {
      if (!element || !editor) return;
      const columns = element.clientWidth >= 700 ? 2 : 1;
      const heading = element.querySelector<HTMLElement>(".agent-skills-summary-heading");
      const precedingHeight = Math.max(
        0,
        element.getBoundingClientRect().top - editor.getBoundingClientRect().top + editor.scrollTop,
      );
      const height = Math.max(112, editor.clientHeight - precedingHeight - (heading?.offsetHeight ?? 64) - 76);
      const rows = Math.max(1, Math.floor((height + 16) / 128));
      element.style.setProperty("--resource-columns", String(columns));
      onCapacityChange.current?.(Math.min(100, columns * rows));
    }
    const observer = new ResizeObserver(() => {
      clearTimeout(timer);
      timer = setTimeout(measure, 150);
    });
    observer.observe(editor);
    observer.observe(element);
    measure();
    return () => {
      observer.disconnect();
      clearTimeout(timer);
    };
  }, [enabled, paused]);
  return ref;
}
