import { describe, it, expect } from 'vitest';
import {
  providerLabel,
  providerIconText,
  channelDisplayLabel,
  filterPostableChannels,
  buildChannelPickerOptions,
  channelRoles,
  roleBadgeClass,
  roleBadgeLabel,
} from './channelHelpers';
import type { Channel, Integration } from '../../types';

const slackIntegration: Integration = {
  id: 1,
  uuid: 'int-slack',
  provider: 'slack',
  name: 'Primary Slack',
  credentials: {},
  enabled: true,
  created_at: '',
  updated_at: '',
};

const telegramIntegration: Integration = {
  id: 2,
  uuid: 'int-tg',
  provider: 'telegram',
  name: 'TG Bot',
  credentials: {},
  enabled: true,
  created_at: '',
  updated_at: '',
};

const makeChannel = (overrides: Partial<Channel>): Channel => ({
  id: overrides.id ?? 0,
  uuid: overrides.uuid ?? 'uuid-' + Math.random().toString(36).slice(2),
  integration_id: overrides.integration_id ?? slackIntegration.id,
  external_id: overrides.external_id ?? 'C_DEFAULT',
  display_name: overrides.display_name ?? '',
  can_post: overrides.can_post ?? false,
  can_listen: overrides.can_listen ?? false,
  is_default_post: overrides.is_default_post ?? false,
  extraction_prompt: overrides.extraction_prompt ?? '',
  process_bot_messages: true,
  process_human_messages: overrides.process_human_messages ?? false,
  enabled: overrides.enabled ?? true,
  integration: overrides.integration ?? slackIntegration,
  created_at: '',
  updated_at: '',
});

describe('providerLabel', () => {
  it('renders friendly labels for known providers', () => {
    expect(providerLabel('slack')).toBe('Slack');
    expect(providerLabel('telegram')).toBe('Telegram');
  });

  it('falls back to the raw provider string', () => {
    expect(providerLabel('discord')).toBe('discord');
  });
});

describe('providerIconText', () => {
  it('uses two-letter codes for known providers', () => {
    expect(providerIconText('slack')).toBe('SL');
    expect(providerIconText('telegram')).toBe('TG');
  });

  it('falls back to first two letters of unknown providers', () => {
    expect(providerIconText('discord')).toBe('DI');
  });
});

describe('channelDisplayLabel', () => {
  it('prefers display_name when set', () => {
    expect(channelDisplayLabel(makeChannel({ display_name: '#alerts', external_id: 'C_ABC' }))).toBe('#alerts');
  });

  it('falls back to external_id when display_name is empty', () => {
    expect(channelDisplayLabel(makeChannel({ display_name: '', external_id: 'C_XYZ' }))).toBe('C_XYZ');
  });

  it('treats whitespace-only display_name as empty', () => {
    expect(channelDisplayLabel(makeChannel({ display_name: '   ', external_id: 'C_FB' }))).toBe('C_FB');
  });
});

describe('filterPostableChannels', () => {
  it('keeps only enabled channels with can_post=true', () => {
    const channels = [
      makeChannel({ uuid: 'a', can_post: true, enabled: true }),
      makeChannel({ uuid: 'b', can_post: false, enabled: true }),
      makeChannel({ uuid: 'c', can_post: true, enabled: false }),
      makeChannel({ uuid: 'd', can_post: true, enabled: true }),
    ];
    expect(filterPostableChannels(channels).map((c) => c.uuid)).toEqual(['a', 'd']);
  });
});

describe('buildChannelPickerOptions', () => {
  it('filters listener-only channels out so the picker only offers post targets', () => {
    const channels = [
      makeChannel({ uuid: 'post', can_post: true, display_name: '#alerts' }),
      makeChannel({ uuid: 'listen', can_listen: true, display_name: '#mentions' }),
    ];
    const opts = buildChannelPickerOptions(channels);
    expect(opts.map((o) => o.uuid)).toEqual(['post']);
  });

  it('places the per-provider default first within its provider group', () => {
    const channels = [
      makeChannel({ uuid: 'staging', can_post: true, display_name: '#staging' }),
      makeChannel({ uuid: 'default', can_post: true, is_default_post: true, display_name: '#alerts' }),
      makeChannel({ uuid: 'misc', can_post: true, display_name: '#misc' }),
    ];
    const opts = buildChannelPickerOptions(channels);
    expect(opts.map((o) => o.uuid)).toEqual(['default', 'misc', 'staging']);
    expect(opts[0].isDefault).toBe(true);
  });

  it('groups options by provider, slack before telegram alphabetically', () => {
    const channels = [
      makeChannel({
        uuid: 'tg',
        integration_id: telegramIntegration.id,
        integration: telegramIntegration,
        can_post: true,
        display_name: 'TG Chat',
      }),
      makeChannel({
        uuid: 'sl',
        can_post: true,
        display_name: '#slack',
      }),
    ];
    const opts = buildChannelPickerOptions(channels);
    expect(opts.map((o) => o.provider)).toEqual(['slack', 'telegram']);
  });

  it('uses display_name then external_id as the option label', () => {
    const channels = [
      makeChannel({ uuid: 'a', can_post: true, display_name: '#alerts', external_id: 'C_AAA' }),
      makeChannel({ uuid: 'b', can_post: true, display_name: '', external_id: 'C_BBB' }),
    ];
    const opts = buildChannelPickerOptions(channels);
    expect(opts.find((o) => o.uuid === 'a')!.label).toBe('#alerts');
    expect(opts.find((o) => o.uuid === 'b')!.label).toBe('C_BBB');
  });
});

describe('channelRoles', () => {
  it('emits default badge instead of post when the channel is the default', () => {
    const ch = makeChannel({ can_post: true, is_default_post: true });
    expect(channelRoles(ch)).toEqual(['default']);
  });

  it('emits post badge when can_post is set but the channel is not default', () => {
    const ch = makeChannel({ can_post: true });
    expect(channelRoles(ch)).toEqual(['post']);
  });

  it('combines listen with post / default', () => {
    expect(channelRoles(makeChannel({ can_post: true, can_listen: true }))).toEqual(['post', 'listen']);
    expect(
      channelRoles(makeChannel({ can_post: true, is_default_post: true, can_listen: true })),
    ).toEqual(['default', 'listen']);
  });

  it('marks disabled channels first so the badge is visible', () => {
    const ch = makeChannel({ can_post: true, enabled: false });
    expect(channelRoles(ch)).toEqual(['disabled', 'post']);
  });

  it('returns empty array when no capabilities are set on an enabled channel', () => {
    expect(channelRoles(makeChannel({}))).toEqual([]);
  });
});

describe('roleBadgeClass / roleBadgeLabel', () => {
  it('returns a class string for each role', () => {
    expect(roleBadgeClass('default')).toContain('badge');
    expect(roleBadgeClass('post')).toContain('badge');
    expect(roleBadgeClass('listen')).toContain('badge');
    expect(roleBadgeClass('disabled')).toContain('badge');
  });

  it('returns a human-friendly label for each role', () => {
    expect(roleBadgeLabel('default')).toBe('Default post');
    expect(roleBadgeLabel('post')).toBe('Post');
    expect(roleBadgeLabel('listen')).toBe('Listen');
    expect(roleBadgeLabel('disabled')).toBe('Disabled');
  });
});
