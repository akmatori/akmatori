import { describe, it, expect } from 'vitest';
import {
  SLACK_CHANNEL_TYPE_NAME,
  visibleAlertSourceTypes,
  isWebhookSourceType,
} from './alertSourceHelpers';
import type { AlertSourceType } from '../../types';

const t = (overrides: Partial<AlertSourceType>): AlertSourceType => ({
  id: overrides.id ?? 0,
  name: overrides.name ?? '',
  display_name: overrides.display_name ?? '',
  description: overrides.description ?? '',
  default_field_mappings: overrides.default_field_mappings ?? {},
  webhook_secret_header: overrides.webhook_secret_header ?? '',
  deprecated: overrides.deprecated,
  created_at: '',
  updated_at: '',
});

describe('visibleAlertSourceTypes', () => {
  it('hides the deprecated slack_channel type so the picker no longer offers it', () => {
    const types = [
      t({ id: 1, name: 'alertmanager' }),
      t({ id: 2, name: SLACK_CHANNEL_TYPE_NAME }),
      t({ id: 3, name: 'grafana' }),
    ];
    expect(visibleAlertSourceTypes(types).map((x) => x.name)).toEqual(['alertmanager', 'grafana']);
  });

  it('also hides server-flagged deprecated types so backend deprecation works', () => {
    const types = [
      t({ id: 1, name: 'alertmanager' }),
      t({ id: 2, name: 'old_type', deprecated: true }),
    ];
    expect(visibleAlertSourceTypes(types).map((x) => x.name)).toEqual(['alertmanager']);
  });

  it('returns the input unchanged when no deprecated types are present', () => {
    const types = [t({ id: 1, name: 'alertmanager' }), t({ id: 2, name: 'grafana' })];
    expect(visibleAlertSourceTypes(types)).toHaveLength(2);
  });
});

describe('isWebhookSourceType', () => {
  it('treats every non-slack-channel type as webhook so the secret field renders', () => {
    expect(isWebhookSourceType('alertmanager')).toBe(true);
    expect(isWebhookSourceType('pagerduty')).toBe(true);
  });

  it('returns false for slack_channel (now replaced by Channels)', () => {
    expect(isWebhookSourceType(SLACK_CHANNEL_TYPE_NAME)).toBe(false);
  });

  it('returns false for the empty string so the secret hides until a type is picked', () => {
    expect(isWebhookSourceType('')).toBe(false);
  });
});
