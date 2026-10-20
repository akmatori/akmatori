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

  it('marks Telegram as unavailable so the Add button is disabled with a coming-soon hint', () => {
    const tg = getProviderConfig('telegram');
    expect(tg).not.toBeNull();
    expect(tg!.available).toBe(false);
    expect(tg!.description.toLowerCase()).toContain('not yet available');
  });

  it('marks Zulip as available with the bot credentials required by its API', () => {
    const zulip = getProviderConfig('zulip');
    expect(zulip).not.toBeNull();
    expect(zulip!.available).toBe(true);
    expect(zulip!.credentialFields.map((field) => field.name)).toEqual(['site_url', 'email', 'api_key']);
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
    expect(getProviderConfig('discord')).toBeNull();
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
