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
    <div className="flex items-center justify-between px-4 py-3 border-t border-gray-200 dark:border-gray-700">
      <div className="flex items-center gap-4">
        <span className="text-sm text-gray-500 dark:text-gray-400">
          Page {page} of {totalPages} ({totalItems.toLocaleString()} total)
        </span>
        {perPage !== undefined && onPerPageChange && (
          <select
            value={perPage}
            onChange={(e) => onPerPageChange(Number(e.target.value))}
            className="text-sm border border-gray-300 dark:border-gray-600 rounded px-2 py-1 bg-white dark:bg-gray-800 text-gray-700 dark:text-gray-300"
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
          className="btn btn-secondary p-1.5 disabled:opacity-50"
          aria-label="Previous page"
        >
          <ChevronLeft className="w-4 h-4" />
        </button>
        <button
          onClick={() => onPageChange(page + 1)}
          disabled={page >= totalPages}
          className="btn btn-secondary p-1.5 disabled:opacity-50"
          aria-label="Next page"
        >
          <ChevronRight className="w-4 h-4" />
        </button>
      </div>
    </div>
  );
}
