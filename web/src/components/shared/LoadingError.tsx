import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';

interface LoadingErrorProps {
  loading: boolean;
  error: string | null;
  children: React.ReactNode;
}

export default function LoadingError({ loading, error, children }: LoadingErrorProps) {
  if (loading) {
    return <LoadingSpinner />;
  }

  return (
    <>
      {error && <ErrorMessage message={error} />}
      {children}
    </>
  );
}
