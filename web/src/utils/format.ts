// Shared display formatters. Pages used to carry private copies of these;
// keep every output string byte-identical to what those copies produced.

// formatRelativeShort renders the age of an ISO timestamp as "5s", "3m",
// "2h", "4d" or "1w" with no suffix (table columns).
export function formatRelativeShort(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const diffSec = Math.max(0, Math.floor(diffMs / 1000));
  if (diffSec < 60) return `${diffSec}s`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d`;
  const diffWeek = Math.floor(diffDay / 7);
  return `${diffWeek}w`;
}

// formatRelativeAgo is formatRelativeShort with an " ago" suffix (feed rows).
export function formatRelativeAgo(iso: string): string {
  return `${formatRelativeShort(iso)} ago`;
}

// formatAbsolute renders an ISO timestamp in the browser locale.
export function formatAbsolute(iso: string): string {
  return new Date(iso).toLocaleString();
}

// formatShortDateTime renders "Jan 5, 2026, 03:04 PM" style timestamps.
export function formatShortDateTime(iso: string): string {
  return new Date(iso).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// formatCountdown renders the time left until an ISO timestamp, or "expired".
export function formatCountdown(iso: string): string {
  const remainingMs = new Date(iso).getTime() - Date.now();
  if (remainingMs <= 0) return 'expired';
  const remainingMin = Math.floor(remainingMs / 60000);
  if (remainingMin < 60) return `${remainingMin}m left`;
  const remainingHr = Math.floor(remainingMin / 60);
  const remMin = remainingMin % 60;
  if (remainingHr < 24) return `${remainingHr}h ${remMin}m left`;
  const remainingDay = Math.floor(remainingHr / 24);
  return `${remainingDay}d left`;
}

// formatExecutionTime renders a millisecond duration as "850ms", "12.3s",
// "4m 5s" or "2h 7m". Zero or missing values render as "-".
export function formatExecutionTime(ms: number): string {
  if (!ms || ms <= 0) return '-';
  if (ms < 1000) return `${ms}ms`;
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes < 60) return `${minutes}m ${Math.round(remainingSeconds)}s`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return `${hours}h ${remainingMinutes}m`;
}

// formatTokens renders a token count with thousands separators, or "-".
export function formatTokens(tokens: number): string {
  if (!tokens || tokens <= 0) return '-';
  return tokens.toLocaleString();
}

// formatFileSize renders a byte count as B, KB or MB.
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
