import { useState, useEffect, useCallback } from 'react';
import { llmSettingsApi } from '../api/client';

const ONBOARDING_DISMISSED_KEY = 'akmatori_onboarding_dismissed';

interface SetupStatus {
  isLoading: boolean;
  isConfigured: boolean;
  showOnboarding: boolean;
  dismissOnboarding: () => void;
  markComplete: () => void;
  recheckStatus: () => Promise<void>;
}

export function useSetupStatus(): SetupStatus {
  const [isLoading, setIsLoading] = useState(true);
  const [isConfigured, setIsConfigured] = useState(false);
  const [dismissed, setDismissed] = useState(() => {
    return localStorage.getItem(ONBOARDING_DISMISSED_KEY) === 'true';
  });

  const checkStatus = useCallback(async () => {
    try {
      setIsLoading(true);
      const settings = await llmSettingsApi.get();
      setIsConfigured(settings.is_configured);
    } catch (err) {
      // If we can't check, assume not configured
      setIsConfigured(false);
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    checkStatus();
  }, [checkStatus]);

  const dismissOnboarding = useCallback(() => {
    setDismissed(true);
    localStorage.setItem(ONBOARDING_DISMISSED_KEY, 'true');
  }, []);

  const markComplete = useCallback(() => {
    setIsConfigured(true);
    setDismissed(true);
    localStorage.setItem(ONBOARDING_DISMISSED_KEY, 'true');
  }, []);

  return {
    isLoading,
    isConfigured,
    showOnboarding: !isLoading && !isConfigured && !dismissed,
    dismissOnboarding,
    markComplete,
    recheckStatus: checkStatus,
  };
}
