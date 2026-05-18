import type { MessagingProvider } from '../../types';

// ProviderUIConfig captures everything the IntegrationsManager UI needs to
// know to render a provider's row: display label, whether the "Add" button
// is enabled (the data model already supports a provider even when the
// runtime is a stub), and the description string used in tooltips.
export interface ProviderUIConfig {
  provider: MessagingProvider;
  label: string;
  iconText: string;
  available: boolean;
  description: string;
  // Form fields rendered in the credentials section. Each entry's name is the
  // JSONB key the backend persists; secret fields are rendered with type
  // password and masked on read.
  credentialFields: Array<{
    name: string;
    label: string;
    secret?: boolean;
    placeholder?: string;
    hint?: string;
  }>;
}

// PROVIDER_CONFIGS is the single source of truth for which providers the UI
// supports and which are "coming soon". Telegram is intentionally present but
// disabled — the data model accepts a Telegram integration so operators can
// pre-configure one, but the IntegrationsManager UI marks it as not yet ready.
export const PROVIDER_CONFIGS: ProviderUIConfig[] = [
  {
    provider: 'slack',
    label: 'Slack',
    iconText: 'SL',
    available: true,
    description: 'Connect a Slack workspace for inbound alert listening and outbound notifications.',
    credentialFields: [
      {
        name: 'bot_token',
        label: 'Bot Token',
        secret: true,
        placeholder: 'xoxb-...',
        hint: 'Used to post messages and listen for thread mentions.',
      },
      {
        name: 'signing_secret',
        label: 'Signing Secret',
        secret: true,
        placeholder: 'Signing secret from Slack app settings',
      },
      {
        name: 'app_token',
        label: 'App Token',
        secret: true,
        placeholder: 'xapp-...',
        hint: 'Required for Socket Mode (live event delivery).',
      },
    ],
  },
  {
    provider: 'telegram',
    label: 'Telegram',
    iconText: 'TG',
    available: false,
    description: 'Telegram integration is not yet available. The data model is ready for when the provider lands.',
    credentialFields: [
      {
        name: 'bot_token',
        label: 'Bot Token',
        secret: true,
        placeholder: 'Available in a future release',
      },
    ],
  },
];

// getProviderConfig returns the UI config for a provider, or null when no
// known config exists. Callers should treat a null return as "unknown
// provider" and render a generic fallback rather than crashing.
export function getProviderConfig(provider: MessagingProvider | string): ProviderUIConfig | null {
  return PROVIDER_CONFIGS.find((c) => c.provider === provider) ?? null;
}

// extractCredentialsForCreate folds the form state's per-field values into the
// JSONB-shaped credentials object the backend expects on POST. Empty strings
// are dropped so partial submissions don't overwrite existing rows with empty
// values — the backend treats missing keys as "no change".
export function extractCredentialsForCreate(
  config: ProviderUIConfig,
  values: Record<string, string>,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const field of config.credentialFields) {
    const v = (values[field.name] ?? '').trim();
    if (v !== '') {
      out[field.name] = v;
    }
  }
  return out;
}

// areCredentialsValidForCreate returns whether every required credential field
// has been filled in for create. Update is a partial patch so this is not
// used there. For Slack, all three secrets are required to mark the
// integration "configured" downstream.
export function areCredentialsValidForCreate(
  config: ProviderUIConfig,
  values: Record<string, string>,
): boolean {
  return config.credentialFields.every((f) => (values[f.name] ?? '').trim() !== '');
}
