import { ChevronLeft, ChevronRight } from 'lucide-react';

interface PaginationProps {
  page: number;
  totalPages: number;
  totalItems: number;
  onPageChange: (page: number) => void;
  // When provided, a per-page selector is rendered next to the summary.
  perPage?: number;
  perPageOptions?: number[];
  onPerPageChange?: (perPage: number) => void;
}

// Pagination renders the table footer with a "Page X of Y" summary and
// previous/next buttons. It renders nothing for a single page.
export default function Pagination({
  page,
  totalPages,
  totalItems,
  onPageChange,
  perPage,
  perPageOptions = [25, 50, 100],
  onPerPageChange,
}: PaginationProps) {
  if (totalPages <= 1) return null;

  return (
    <div className="flex items-center justify-between flex-wrap gap-3 px-4 py-3 border-t border-line">
      <div className="flex items-center gap-4">
        <span className="text-sm text-fg-subtle">
          Page {page} of {totalPages} ({totalItems.toLocaleString()} total)
        </span>
        {perPage !== undefined && onPerPageChange && (
          <select
            value={perPage}
            onChange={(e) => onPerPageChange(Number(e.target.value))}
            aria-label="Rows per page"
            className="input-field h-8 w-auto py-0 text-sm"
          >
            {perPageOptions.map((n) => (
              <option key={n} value={n}>{n} per page</option>
            ))}
          </select>
        )}
      </div>
      <div className="flex items-center gap-2">
        <button
          onClick={() => onPageChange(page - 1)}
          disabled={page <= 1}
          className="btn btn-secondary w-8 h-8 p-0"
          aria-label="Previous page"
        >
          <ChevronLeft className="w-4 h-4" />
        </button>
        <button
          onClick={() => onPageChange(page + 1)}
          disabled={page >= totalPages}
          className="btn btn-secondary w-8 h-8 p-0"
          aria-label="Next page"
        >
          <ChevronRight className="w-4 h-4" />
        </button>
      </div>
    </div>
  );
}
