import { createContext, useContext, useState, useEffect, useCallback, type ReactNode } from 'react';
import type { AuthUser, LoginResponse, SetupStatusResponse } from '../types';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '';
const TOKEN_KEY = 'aiops_auth_token';
const USER_KEY = 'aiops_auth_user';

interface AuthContextType {
  user: AuthUser | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  setupRequired: boolean;
  login: (username: string, password: string) => Promise<void>;
  completeSetup: (password: string, confirmPassword: string) => Promise<void>;
  logout: () => void;
  getToken: () => string | null;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [setupRequired, setSetupRequired] = useState(false);

  // Initialize auth state from localStorage
  useEffect(() => {
    const initAuth = async () => {
      try {
        // Check setup status first
        const setupRes = await fetch(`${API_BASE_URL}/auth/setup-status`);
        if (setupRes.ok) {
          const setupStatus: SetupStatusResponse = await setupRes.json();
          if (setupStatus.setup_required) {
            setSetupRequired(true);
            setIsLoading(false);
            return;
          }
        }
      } catch {
        // If setup-status fails (e.g., network error), continue with normal auth flow
      }

      const token = localStorage.getItem(TOKEN_KEY);
      const username = localStorage.getItem(USER_KEY);

      if (token && username) {
        // Verify token is still valid
        try {
          const response = await fetch(`${API_BASE_URL}/auth/verify`, {
            headers: {
              Authorization: `Bearer ${token}`,
            },
          });
          if (response.ok) {
            setUser({ token, username });
          } else {
            // Token expired or invalid - clear storage
            localStorage.removeItem(TOKEN_KEY);
            localStorage.removeItem(USER_KEY);
          }
        } catch {
          // Network error - clear storage
          localStorage.removeItem(TOKEN_KEY);
          localStorage.removeItem(USER_KEY);
        }
      }
      setIsLoading(false);
    };

    initAuth();
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    let response: Response;
    try {
      response = await fetch(`${API_BASE_URL}/auth/login`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ username, password }),
      });
    } catch {
      // fetch only rejects on network-level failures (server unreachable, DNS, CORS)
      throw new Error('Cannot reach the Akmatori server. Check your connection and try again.');
    }

    if (!response.ok) {
      // 502/503/504 come from the reverse proxy when the API is down or restarting,
      // and carry an HTML body — distinguish them from genuine auth failures.
      if (response.status === 502 || response.status === 503 || response.status === 504) {
        throw new Error('The Akmatori API is currently unavailable. Please try again in a moment.');
      }
      const data = await response.json().catch(() => ({}));
      throw new Error(data.error || 'Login failed');
    }

    const data: LoginResponse = await response.json();

    // Store in localStorage
    localStorage.setItem(TOKEN_KEY, data.token);
    localStorage.setItem(USER_KEY, data.username);

    setUser({ token: data.token, username: data.username });
  }, []);

  const completeSetup = useCallback(async (password: string, confirmPassword: string) => {
    let response: Response;
    try {
      response = await fetch(`${API_BASE_URL}/auth/setup`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ password, confirm_password: confirmPassword }),
      });
    } catch {
      throw new Error('Cannot reach the Akmatori server. Check your connection and try again.');
    }

    if (!response.ok) {
      if (response.status === 502 || response.status === 503 || response.status === 504) {
        throw new Error('The Akmatori API is currently unavailable. Please try again in a moment.');
      }
      const data = await response.json().catch(() => ({}));
      // Handle validation errors
      if (data.details) {
        const firstError = Object.values(data.details)[0];
        throw new Error(firstError as string);
      }
      throw new Error(data.error || 'Setup failed');
    }

    const data: LoginResponse = await response.json();

    // Store in localStorage
    localStorage.setItem(TOKEN_KEY, data.token);
    localStorage.setItem(USER_KEY, data.username);

    setSetupRequired(false);
    setUser({ token: data.token, username: data.username });
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USER_KEY);
    setUser(null);
  }, []);

  const getToken = useCallback(() => {
    return localStorage.getItem(TOKEN_KEY);
  }, []);

  return (
    <AuthContext.Provider
      value={{
        user,
        isAuthenticated: !!user,
        isLoading,
        setupRequired,
        login,
        completeSetup,
        logout,
        getToken,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
