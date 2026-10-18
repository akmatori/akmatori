import { createContext } from 'react';
import type { AuthUser } from '../types';

export interface AuthContextType {
  user: AuthUser | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  setupRequired: boolean;
  login: (username: string, password: string) => Promise<void>;
  completeSetup: (password: string, confirmPassword: string) => Promise<void>;
  logout: () => void;
  getToken: () => string | null;
}

export const AuthContext = createContext<AuthContextType | undefined>(undefined);
