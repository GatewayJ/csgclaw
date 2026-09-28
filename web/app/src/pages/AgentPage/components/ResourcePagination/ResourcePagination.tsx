import { Button } from "@/components/ui";
import type { TranslateFn } from "@/models/conversations";
import type { ResourcePagination as PaginationState } from "@/hooks/workspace/useAgentResourcePages";
import styles from "./ResourcePagination.module.css";

export function ResourcePagination({ pagination, t }: { pagination?: PaginationState; t: TranslateFn }) {
  if (!pagination) return null;
  const pages = Math.max(1, Math.ceil(pagination.total / pagination.per));
  return (
    <nav className={styles.pagination} aria-label={t("resourcePaginationLabel")}>
      <Button
        variant="secondaryGray"
        size="sm"
        disabled={pagination.loading || pagination.page <= 1}
        onClick={() => pagination.onPageChange(pagination.page - 1)}
      >
        {t("resourcePreviousPage")}
      </Button>
      <span role="status">{t("resourcePagePosition", { page: pagination.page, pages, total: pagination.total })}</span>
      <Button
        variant="secondaryGray"
        size="sm"
        disabled={pagination.loading || pagination.page >= pages}
        onClick={() => pagination.onPageChange(pagination.page + 1)}
      >
        {t("resourceNextPage")}
      </Button>
    </nav>
  );
}
