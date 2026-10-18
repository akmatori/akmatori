export default function LoadingSpinner() {
  return (
    <div role="status" className="flex items-center justify-center gap-3 p-12 text-sm text-fg-subtle">
      <span className="relative w-5 h-5" aria-hidden="true">
        <span className="absolute inset-0 rounded-full border-2 border-line" />
        <span className="absolute inset-0 rounded-full border-2 border-transparent border-t-primary-500 animate-spin" />
      </span>
      Loading…
    </div>
  );
}
