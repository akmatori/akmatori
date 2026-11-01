import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Plus, Sparkles } from 'lucide-react';
import { channelsApi, formattingRulesApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';
import type { Channel, FormattingRule } from '../../types';
import type { ConnectorDetail } from '../../types/connectors';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { newRuleLink, rulesForTarget } from './connectorHelpers';

const PLATFORM_NOTES: Record<string, string> = {
  slack: 'Slack: Markdown is converted to Slack mrkdwn; long messages are cut to Slack’s size limit.',
  telegram: 'Telegram: Markdown is converted to Telegram HTML; messages over 4096 characters are split.',
  discord: 'Discord: Markdown is sent as is; messages over 2000 characters are split, code blocks stay intact.',
};

interface Target {
  key: string;
  label: string;
  hint: string;
  channelUUID?: string;
  sourceUUID?: string;
}

function RuleList({ rules, target }: { rules: FormattingRule[]; target: Target }) {
  const { specific, catchAll } = rulesForTarget(rules, target);
  const first = specific[0] ?? catchAll[0];
  return (
    <div className="flex items-start justify-between gap-4 py-3">
      <div className="min-w-0">
        <p className="text-sm text-fg">{target.label}</p>
        <p className="text-xs text-fg-subtle">{target.hint}</p>
        {specific.length + catchAll.length === 0 ? (
          <p className="mt-1 text-xs text-fg-muted">No rule applies: the agent’s answer is sent as written.</p>
        ) : (
          <ul className="mt-1 space-y-0.5">
            {specific.map((r) => (
              <li key={r.uuid} className="text-xs text-fg-muted">
                <span className={r === first ? 'text-fg' : ''}>{r.name}</span>
                {r.match_expression ? ' (expression rule)' : ''}
              </li>
            ))}
            {catchAll.map((r) => (
              <li key={r.uuid} className="text-xs text-fg-muted">
                <span className={r === first ? 'text-fg' : ''}>{r.name}</span> (applies to everything)
              </li>
            ))}
            {specific.length + catchAll.length > 1 && (
              <li className="text-xs text-fg-subtle">The first rule in the list whose conditions match wins; other conditions such as the skill can change which one.</li>
            )}
          </ul>
        )}
      </div>
      <Link to={newRuleLink({ ...target, name: target.label })} className="btn btn-ghost flex-shrink-0 text-xs">
        <Plus className="w-3.5 h-3.5" /> Rule for this
      </Link>
    </div>
  );
}

// FormattingPanel shows how responses look when they go through this
// connector. Two layers: formatting rules (an LLM rewrites the content; the
// operator configures them) and platform rendering (automatic per provider).
export default function FormattingPanel({ connector }: { connector: ConnectorDetail }) {
  const [rules, setRules] = useState<FormattingRule[] | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    Promise.all([formattingRulesApi.list(), channelsApi.list().catch(() => [] as Channel[])])
      .then(([r, c]) => {
        if (cancelled) return;
        setRules(r);
        setChannels(c);
      })
      .catch((err) => !cancelled && setError(getErrorMessage(err, 'Failed to load formatting rules')));
    return () => {
      cancelled = true;
    };
  }, []);

  if (error) return <ErrorMessage message={error} />;
  if (!rules) return <LoadingSpinner />;

  const integrationIDs = new Set(connector.parts.integrations.map((i) => i.id));
  const providers = [...new Set(connector.parts.integrations.map((i) => i.provider))];
  const targets: Target[] = [
    ...channels
      .filter((c) => integrationIDs.has(c.integration_id) && c.can_post)
      .map((c) => ({ key: `c-${c.uuid}`, label: c.display_name || c.external_id, hint: 'Destination channel', channelUUID: c.uuid })),
    ...connector.parts.alert_sources.map((s) => ({
      key: `s-${s.uuid}`,
      label: s.name,
      hint: 'Alerts from this source (wherever the result is posted)',
      sourceUUID: s.uuid,
    })),
  ];

  return (
    <div className="space-y-6">
      <section className="card space-y-2">
        <h2 className="text-sm font-semibold text-fg flex items-center gap-2">
          <Sparkles className="w-4 h-4 text-fg-subtle" aria-hidden="true" /> How responses are formatted
        </h2>
        <ol className="list-decimal pl-5 space-y-1 text-sm text-fg-muted">
          <li>
            <span className="text-fg">Formatting rules</span> rewrite the agent’s answer into the structure you want
            (an LLM call). The first enabled rule that matches the flow wins.
          </li>
          <li>
            <span className="text-fg">Platform rendering</span> is automatic: the same Markdown is adapted to each messaging
            platform and its size limits.
          </li>
        </ol>
        {providers.map((p) => PLATFORM_NOTES[p] && <p key={p} className="text-xs text-fg-subtle">{PLATFORM_NOTES[p]}</p>)}
        <p className="text-xs">
          <Link to="/settings/formatting" className="text-primary-500 hover:underline">Open all formatting rules</Link>
        </p>
      </section>

      <section className="card">
        <h2 className="text-sm font-semibold text-fg">Rules for this connector</h2>
        {targets.length === 0 ? (
          <p className="mt-2 text-sm text-fg-muted">This connector has no channels or alert sources yet.</p>
        ) : (
          <div className="divide-y divide-line">
            {targets.map((t) => (
              <RuleList key={t.key} rules={rules} target={t} />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
