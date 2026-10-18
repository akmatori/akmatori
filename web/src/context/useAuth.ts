import { useContext } from 'react';
import { AuthContext, type AuthContextType } from './auth-context';

export function useAuth(): AuthContextType {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
