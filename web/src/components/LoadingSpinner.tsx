export default function LoadingSpinner() {
  return (
    <div className="flex flex-col items-center justify-center p-12 gap-4">
      {/* Clean loading spinner */}
      <div className="relative">
        <div className="w-10 h-10 rounded-full border-3 border-gray-200 dark:border-gray-700" />
        <div className="absolute inset-0 rounded-full border-3 border-transparent border-t-primary-500 animate-spin" />
      </div>

      {/* Loading text */}
      <p className="text-sm text-gray-500 dark:text-gray-400">
        Loading...
      </p>
    </div>
  );
}
