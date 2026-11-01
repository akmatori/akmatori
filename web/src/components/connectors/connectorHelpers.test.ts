import { describe, expect, it } from 'vitest';
import {
  addPathFor,
  filterConnectors,
  groupCatalog,
  isValidRemoteURL,
  monogram,
  newRuleLink,
  rulesForTarget,
  slugNamespace,
  statusView,
} from './connectorHelpers';
import { creatableAlertTypes, creatableToolTypes, scopedAlertSources, scopedTools } from './partScope';
import type { ConnectorCatalogEntry, ConnectorSummary } from '../../types/connectors';
import type { AlertSourceInstance, AlertSourceType, FormattingRule, ToolInstance, ToolType } from '../../types';

const entry = (over: Partial<ConnectorCatalogEntry>): ConnectorCatalogEntry => ({
  id: 'x',
  name: 'X',
  description: '',
  category: 'other',
  kind: 'builtin',
  capabilities: [],
  ...over,
});

const rule = (over: Partial<FormattingRule>): FormattingRule => ({
  id: 1,
  uuid: 'r',
  name: 'r',
  enabled: true,
  position: 0,
  match_source_kind: '',
  match_source_uuid: '',
  match_channel_uuid: '',
  match_last_skill: '',
  match_expression: '',
  system_prompt: '',
  output_schema_example: '',
  max_tokens: 0,
  temperature: 0,
  created_at: '',
  updated_at: '',
  ...over,
});

describe('statusView', () => {
  it('maps states to labels and tones', () => {
    expect(statusView('connected')).toEqual({ label: 'Connected', tone: 'success' });
    expect(statusView('needs_setup').tone).toBe('warning');
    expect(statusView('error').tone).toBe('danger');
    expect(statusView('unknown').label).toBe('Unknown');
  });
});

describe('monogram', () => {
  it('takes initials of two words or two letters of one', () => {
    expect(monogram('Prometheus Alertmanager')).toBe('PA');
    expect(monogram('kubernetes')).toBe('KU');
    expect(monogram('victoria_metrics')).toBe('VM');
    expect(monogram('   ')).toBe('?');
  });
});

describe('groupCatalog', () => {
  it('groups by category in the fixed order and filters by query', () => {
    const groups = groupCatalog(
      [
        entry({ id: 'slack', name: 'Slack', category: 'messaging' }),
        entry({ id: 'zabbix', name: 'Zabbix', category: 'monitoring' }),
        entry({ id: 'weird', name: 'Weird', category: 'zzz' }),
      ],
      '',
    );
    expect(groups.map((g) => g.category)).toEqual(['monitoring', 'messaging', 'zzz']);
    expect(groupCatalog([entry({ name: 'Zabbix', category: 'monitoring' })], 'slack')).toEqual([]);
  });
});

describe('filterConnectors', () => {
  it('matches name and catalog name', () => {
    const list = [
      { name: 'Prod', catalog_name: 'Zabbix', catalog_id: 'zabbix' },
      { name: 'Chat', catalog_name: 'Slack', catalog_id: 'slack' },
    ] as ConnectorSummary[];
    expect(filterConnectors(list, 'zab').map((c) => c.name)).toEqual(['Prod']);
    expect(filterConnectors(list, '')).toHaveLength(2);
  });
});

describe('addPathFor', () => {
  it('opens the MCP dialog for custom MCP and the new page otherwise', () => {
    expect(addPathFor(entry({ kind: 'custom_mcp', id: 'custom_mcp' }))).toBe('/connectors?add=mcp');
    expect(addPathFor(entry({ id: 'http:billing' }))).toBe('/connectors/new/http%3Abilling');
  });
});

describe('rulesForTarget', () => {
  it('splits specific rules from catch-all rules and skips disabled ones', () => {
    const rules = [
      rule({ id: 1, position: 2, match_channel_uuid: 'ch-1' }),
      rule({ id: 2, position: 1 }),
      rule({ id: 3, position: 0, match_expression: 'channel == "ch-1" && skill == "x"' }),
      rule({ id: 4, position: 3, match_channel_uuid: 'ch-1', enabled: false }),
      rule({ id: 5, position: 4, match_source_uuid: 'src-1' }),
      rule({ id: 6, position: 5, match_source_kind: 'cron' }),
    ];
    const res = rulesForTarget(rules, { channelUUID: 'ch-1' });
    expect(res.specific.map((r) => r.id)).toEqual([3, 1]);
    expect(res.catchAll.map((r) => r.id)).toEqual([2]);
    expect(rulesForTarget(rules, { sourceUUID: 'src-1' }).specific.map((r) => r.id)).toEqual([5]);
  });
});

describe('newRuleLink', () => {
  it('encodes the target', () => {
    expect(newRuleLink({ channelUUID: 'c1', name: '#ops' })).toBe('/settings/formatting?new=1&name=%23ops&channel=c1');
    expect(newRuleLink({ sourceUUID: 's1', name: 'AM' })).toContain('source=s1');
  });
});

describe('slugNamespace / isValidRemoteURL', () => {
  it('mirrors the server namespace rule', () => {
    expect(slugNamespace('GitHub Issues')).toBe('ext.github_issues');
    expect(slugNamespace('!!!')).toBe('ext.mcp');
  });
  it('accepts only absolute http(s) URLs without credentials', () => {
    expect(isValidRemoteURL('https://mcp.example.com/mcp')).toBe(true);
    expect(isValidRemoteURL('http://10.0.0.5:8080/mcp')).toBe(true);
    expect(isValidRemoteURL('ftp://x')).toBe(false);
    expect(isValidRemoteURL('https://u:p@x.com')).toBe(false);
    expect(isValidRemoteURL('mcp.example.com')).toBe(false);
  });
});

describe('partScope', () => {
  const scope = { connectorId: 7, connectorUUID: 'c', allowedTypes: ['zabbix'] };
  const tt = (name: string, kind = ''): ToolType => ({ id: name.length, name, kind, description: '', schema: {}, created_at: '', updated_at: '' });
  it('hides MCP-managed and built-in types and limits to allowed types', () => {
    const types = [tt('zabbix'), tt('ssh'), tt('incidents'), tt('ext.github', 'mcp')];
    expect(creatableToolTypes(types).map((t) => t.name)).toEqual(['zabbix', 'ssh']);
    expect(creatableToolTypes(types, scope).map((t) => t.name)).toEqual(['zabbix']);
  });
  it('shows only the connector parts and never managed MCP instances', () => {
    const tools = [
      { id: 1, connector_id: 7, tool_type: tt('zabbix') },
      { id: 2, connector_id: 8, tool_type: tt('zabbix') },
      { id: 3, connector_id: 7, tool_type: tt('ext.x', 'mcp') },
    ] as ToolInstance[];
    expect(scopedTools(tools, scope).map((t) => t.id)).toEqual([1]);
    expect(scopedTools(tools).map((t) => t.id)).toEqual([1, 2]);
    const sources = [{ connector_id: 7 }, { connector_id: 9 }] as AlertSourceInstance[];
    expect(scopedAlertSources(sources, scope)).toHaveLength(1);
    const ats = [{ name: 'zabbix' }, { name: 'datadog' }] as AlertSourceType[];
    expect(creatableAlertTypes(ats, scope).map((t) => t.name)).toEqual(['zabbix']);
  });
});
