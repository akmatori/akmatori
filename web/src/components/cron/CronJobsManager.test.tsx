import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { CronJob } from '../../types';

const api = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  delete: vi.fn(),
  run: vi.fn(),
}));

vi.mock('../../api/client', () => ({ cronJobsApi: api }));
vi.mock('../channels/ChannelPicker', () => ({ default: () => null }));

import CronJobsManager from './CronJobsManager';

const job = (over: Partial<CronJob>): CronJob => ({
  id: 1,
  uuid: 'cron-1',
  name: 'Digest',
  schedule: '*/15 * * * *',
  prompt: 'Summarise',
  is_system: false,
  channel_id: null,
  enabled: true,
  post_results: false,
  last_run_status: '',
  last_run_error: '',
  created_at: '',
  updated_at: '',
  channel: null,
  tools_enabled: true,
  builtin_tools: ['events', 'runbooks', 'memory'],
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
});

describe('CronJobsManager tool fields', () => {
  it('shows connector and built-in tool values in the list', async () => {
    api.list.mockResolvedValue([
      job({ uuid: 'a', name: 'A', builtin_tools: ['memory', 'events'] }),
      job({ uuid: 'b', name: 'B', tools_enabled: false, builtin_tools: [] }),
    ]);
    render(
      <MemoryRouter>
        <CronJobsManager />
      </MemoryRouter>,
    );
    expect(await screen.findByText('Connectors: on')).toBeTruthy();
    expect(screen.getByText('Built-in: Events, Memory')).toBeTruthy();
    expect(screen.getByText('Connectors: off')).toBeTruthy();
    expect(screen.getByText('No built-in tools')).toBeTruthy();
  });

  it('sends tools_enabled and builtin_tools on create', async () => {
    api.list.mockResolvedValue([]);
    api.create.mockResolvedValue(job({}));
    render(
      <MemoryRouter>
        <CronJobsManager />
      </MemoryRouter>,
    );
    fireEvent.click(await screen.findByRole('button', { name: /New cron job/ }));
    fireEvent.change(screen.getByPlaceholderText(/Morning incident digest/), { target: { value: 'Job' } });
    fireEvent.change(screen.getByPlaceholderText(/What should the cron-agent do/), { target: { value: 'Do it' } });
    fireEvent.click(screen.getByRole('button', { name: /Save/ }));
    await waitFor(() => expect(api.create).toHaveBeenCalled());
    const body = api.create.mock.calls[0][0];
    expect(body.tools_enabled).toBe(true);
    expect(body.builtin_tools).toEqual(['events', 'runbooks', 'memory']);
    expect(body).not.toHaveProperty('tool_instance_ids');
  });
});
