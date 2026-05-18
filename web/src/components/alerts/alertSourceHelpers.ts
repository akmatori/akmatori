import type { AlertSourceType } from '../../types';

// SLACK_CHANNEL_TYPE_NAME is the deprecated source type that has been replaced
// by the Channel concept. Existing rows of this type are kept in the DB for
// migration safety, but the UI must hide it from new-source pickers.
export const SLACK_CHANNEL_TYPE_NAME = 'slack_channel';

// visibleAlertSourceTypes returns the source types that should appear in the
// "Source Type" picker on the create/edit form. The deprecated slack_channel
// type is filtered out (Channels replace it). Server-side, alert_source_types
// also carry a `deprecated` flag — we respect both signals so the UI degrades
// gracefully whether or not the backend has been updated.
export function visibleAlertSourceTypes(types: AlertSourceType[]): AlertSourceType[] {
  return types.filter((t) => !t.deprecated && t.name !== SLACK_CHANNEL_TYPE_NAME);
}

// isWebhookSourceType reports whether a source-type name represents a webhook
// integration (Alertmanager, Grafana, PagerDuty, Datadog, Zabbix, etc). Used
// to decide whether to show the webhook_secret field. After the slack_channel
// type is retired, all remaining types are webhook types — this guard exists
// for future provider types (e.g. Telegram listeners) that may not be.
export function isWebhookSourceType(typeName: string): boolean {
  return typeName !== '' && typeName !== SLACK_CHANNEL_TYPE_NAME;
}
