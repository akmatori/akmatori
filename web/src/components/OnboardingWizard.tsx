import { useState } from 'react';
import { ArrowRight, Loader2, CheckCircle, Sparkles } from 'lucide-react';
import { llmSettingsApi } from '../api/client';

interface OnboardingWizardProps {
  onComplete: () => void;
  onSkip: () => void;
}

export default function OnboardingWizard({ onComplete, onSkip }: OnboardingWizardProps) {
  const [apiKey, setApiKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!apiKey.trim() || saving) return;

    try {
      setSaving(true);
      setError('');

      const response = await llmSettingsApi.list();
      if (response.active_id) {
        await llmSettingsApi.update(response.active_id, {
          api_key: apiKey.trim(),
        });
      } else if (response.configs.length > 0) {
        await llmSettingsApi.update(response.configs[0].id, {
          api_key: apiKey.trim(),
        });
      } else {
        setError('No LLM configuration found. Please configure one in Settings first.');
        return;
      }

      setSuccess(true);
      setTimeout(() => {
        onComplete();
      }, 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save API key');
    } finally {
      setSaving(false);
    }
  };

  if (success) {
    return (
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
        <div className="bg-surface rounded-2xl shadow-2xl max-w-md w-full p-8 text-center animate-fade-in">
          <div className="w-16 h-16 mx-auto mb-6 rounded-full bg-green-100 dark:bg-green-900/30 flex items-center justify-center">
            <CheckCircle className="w-8 h-8 text-green-600 dark:text-green-400" />
          </div>
          <h2 className="text-2xl font-bold text-fg mb-2">
            You're all set!
          </h2>
          <p className="text-fg-muted">
            Akmatori is ready to investigate incidents.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
      <div className="bg-surface rounded-2xl shadow-2xl max-w-md w-full animate-fade-in">
        {/* Header */}
        <div className="p-8 pb-6 text-center">
          <div className="w-16 h-16 mx-auto mb-6 rounded-full bg-primary-100 dark:bg-primary-900/30 flex items-center justify-center">
            <Sparkles className="w-8 h-8 text-primary-600 dark:text-primary-400" />
          </div>
          <h2 className="text-2xl font-bold text-fg mb-2">
            Welcome to Akmatori
          </h2>
          <p className="text-fg-muted">
            Let's get you started in under a minute.
          </p>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="px-8 pb-8">
          <div className="mb-6">
            <label className="block text-sm font-medium text-fg mb-2">
              LLM API Key
            </label>
            <input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="Enter your API key"
              className="input-field text-lg h-14"
              autoFocus
              disabled={saving}
            />
            <p className="mt-2 text-sm text-fg-subtle">
              Enter an API key for your LLM provider. You can configure the provider in Settings.
            </p>
          </div>

          {error && (
            <div className="mb-4 p-3 bg-state-fail/10 border border-state-fail/40 rounded-lg text-sm text-state-fail">
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={!apiKey.trim() || saving}
            className="w-full h-14 btn btn-primary text-lg justify-center"
          >
            {saving ? (
              <>
                <Loader2 className="w-5 h-5 animate-spin" />
                Connecting...
              </>
            ) : (
              <>
                Get Started
                <ArrowRight className="w-5 h-5" />
              </>
            )}
          </button>

          <button
            type="button"
            onClick={onSkip}
            className="w-full mt-3 text-sm text-fg-subtle hover:text-fg transition-colors"
          >
            Skip for now
          </button>
        </form>
      </div>
    </div>
  );
}
