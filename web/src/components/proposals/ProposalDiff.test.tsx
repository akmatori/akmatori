import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import ProposalDiff from './ProposalDiff';
import type { Proposal } from '../../types';

const proposal = (over: Partial<Proposal>): Proposal => ({
  id: 1,
  uuid: 'p-1',
  kind: 'cron_new',
  status: 'pending',
  title: 'Cron',
  reasoning: '',
  target_ref: '',
  current_snapshot: '',
  proposed_content: '',
  evaluation_run_uuid: '',
  chat_incident_uuid: '',
  created_by: 'agent',
  apply_error: '',
  created_at: '',
  updated_at: '',
  ...over,
});

describe('ProposalDiff cron tool fields', () => {
  it('renders tools_enabled and builtin_tools for a new cron job', () => {
    render(
      <ProposalDiff
        proposal={proposal({
          proposed_content: JSON.stringify({
            name: 'Digest',
            schedule: '0 9 * * *',
            prompt: 'Summarise',
            tools_enabled: true,
            builtin_tools: ['memory', 'events'],
          }),
        })}
      />,
    );
    expect(screen.getByText('Connector tools')).toBeTruthy();
    expect(screen.getByText('Allowed')).toBeTruthy();
    expect(screen.getByText('Built-in tools')).toBeTruthy();
    expect(screen.getByText('Events, Memory')).toBeTruthy();
  });

  it('marks changed cron tool fields on an update', () => {
    render(
      <ProposalDiff
        proposal={proposal({
          kind: 'cron_update',
          current_snapshot: JSON.stringify({ name: 'Digest', tools_enabled: true, builtin_tools: ['events'] }),
          proposed_content: JSON.stringify({ name: 'Digest', tools_enabled: false, builtin_tools: [] }),
        })}
      />,
    );
    expect(screen.getByText('Allowed')).toBeTruthy();
    expect(screen.getByText('Not allowed')).toBeTruthy();
    expect(screen.getByText('Events')).toBeTruthy();
    expect(screen.getByText('None')).toBeTruthy();
    expect(screen.getAllByText('changed')).toHaveLength(2);
  });

  it('still renders legacy tool_logical_names when present', () => {
    render(
      <ProposalDiff
        proposal={proposal({
          proposed_content: JSON.stringify({ name: 'Old', tool_logical_names: ['zabbix', 'grafana'] }),
        })}
      />,
    );
    expect(screen.getByText('Tool logical names')).toBeTruthy();
    expect(screen.getByText('zabbix, grafana')).toBeTruthy();
  });
});
