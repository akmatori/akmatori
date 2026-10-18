// getErrorMessage turns an unknown thrown value into a user-facing string.
// Every API call site used to inline `err instanceof Error ? err.message : '…'`;
// this keeps the fallback explicit and treats an empty message as missing.
export function getErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string' && err) return err;
  return fallback;
}
