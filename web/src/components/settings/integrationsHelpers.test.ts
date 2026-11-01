import { describe, it, expect } from 'vitest';
import {
  PROVIDER_CONFIGS,
  getProviderConfig,
  extractCredentialsForCreate,
  areCredentialsValidForCreate,
} from './integrationsHelpers';

describe('PROVIDER_CONFIGS', () => {
  it('marks Slack as available so the Add button is enabled', () => {
    const slack = getProviderConfig('slack');
    expect(slack).not.toBeNull();
    expect(slack!.available).toBe(true);
  });

  it.each(['telegram', 'discord'] as const)('offers %s as an available outbound-only provider', (provider) => {
    const cfg = getProviderConfig(provider);
    expect(cfg).not.toBeNull();
    expect(cfg!.available).toBe(true);
    expect(cfg!.description.toLowerCase()).toContain('outbound only');
  });

  it.each(['telegram', 'discord'] as const)('asks %s for exactly one secret bot_token field', (provider) => {
    const cfg = getProviderConfig(provider)!;
    expect(cfg.credentialFields).toHaveLength(1);
    expect(cfg.credentialFields[0]).toMatchObject({ name: 'bot_token', label: 'Bot Token', secret: true });
  });

  it('lists all three Slack credentials so the IntegrationsManager renders the right form', () => {
    const slack = getProviderConfig('slack')!;
    const names = slack.credentialFields.map((f) => f.name);
    expect(names).toEqual(expect.arrayContaining(['bot_token', 'signing_secret', 'app_token']));
  });

  it('exposes every config in PROVIDER_CONFIGS via getProviderConfig', () => {
    for (const cfg of PROVIDER_CONFIGS) {
      expect(getProviderConfig(cfg.provider)).toEqual(cfg);
    }
  });

  it('returns null for unknown providers', () => {
    expect(getProviderConfig('matrix')).toBeNull();
  });
});

describe('extractCredentialsForCreate', () => {
  const slack = getProviderConfig('slack')!;

  it('returns the trimmed value for each filled credential field', () => {
    const out = extractCredentialsForCreate(slack, {
      bot_token: ' xoxb-abc ',
      signing_secret: 'ss',
      app_token: 'xapp-1',
    });
    expect(out).toEqual({ bot_token: 'xoxb-abc', signing_secret: 'ss', app_token: 'xapp-1' });
  });

  it('drops empty / whitespace values so the backend receives only changed fields', () => {
    const out = extractCredentialsForCreate(slack, {
      bot_token: 'xoxb-abc',
      signing_secret: '   ',
      app_token: '',
    });
    expect(out).toEqual({ bot_token: 'xoxb-abc' });
  });
});

describe('areCredentialsValidForCreate', () => {
  const slack = getProviderConfig('slack')!;

  it('requires every Slack credential field to be filled', () => {
    expect(
      areCredentialsValidForCreate(slack, {
        bot_token: 'xoxb-abc',
        signing_secret: 'ss',
        app_token: 'xapp-1',
      }),
    ).toBe(true);
  });

  it('rejects partial credential submissions', () => {
    expect(
      areCredentialsValidForCreate(slack, {
        bot_token: 'xoxb-abc',
        signing_secret: '',
        app_token: 'xapp-1',
      }),
    ).toBe(false);
  });

  it('rejects whitespace-only values', () => {
    expect(
      areCredentialsValidForCreate(slack, {
        bot_token: 'xoxb-abc',
        signing_secret: '   ',
        app_token: 'xapp-1',
      }),
    ).toBe(false);
  });
});

describe('bot-token providers', () => {
  it.each(['telegram', 'discord'] as const)('%s requires the bot token on create', (provider) => {
    const cfg = getProviderConfig(provider)!;
    expect(areCredentialsValidForCreate(cfg, {})).toBe(false);
    expect(areCredentialsValidForCreate(cfg, { bot_token: '  ' })).toBe(false);
    expect(areCredentialsValidForCreate(cfg, { bot_token: 'tok' })).toBe(true);
  });

  it.each(['telegram', 'discord'] as const)('%s sends only the bot_token credential', (provider) => {
    const cfg = getProviderConfig(provider)!;
    expect(extractCredentialsForCreate(cfg, { bot_token: ' tok ', app_token: 'ignored' })).toEqual({ bot_token: 'tok' });
    expect(extractCredentialsForCreate(cfg, { bot_token: '' })).toEqual({});
  });
});
