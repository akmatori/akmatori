import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import CronJobForm from './CronJobForm';
import type { CronJob } from '../../types';
import type { CronJobFormState } from './cronJobHelpers';

vi.mock('../channels/ChannelPicker', () => ({ default: () => null }));

const job: CronJob = {
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
  tools_enabled: false,
  builtin_tools: ['incidents'],
};

const checked = (label: string) => (screen.getByLabelText(label) as HTMLInputElement).checked;

function renderForm(props: Partial<React.ComponentProps<typeof CronJobForm>> = {}) {
  const onSave = vi.fn<(f: CronJobFormState) => void>();
  render(
    <MemoryRouter>
      <CronJobForm isCreating onSave={onSave} onCancel={() => {}} {...props} />
    </MemoryRouter>,
  );
  return onSave;
}

describe('CronJobForm tool fields', () => {
  it('sends the create defaults: connector tools on, events + runbooks + memory', async () => {
    const onSave = renderForm();
    fireEvent.change(screen.getByPlaceholderText(/Morning incident digest/), { target: { value: 'Job' } });
    fireEvent.change(screen.getByPlaceholderText(/What should the cron-agent do/), { target: { value: 'Do it' } });

    expect(checked('Allow connector tools')).toBe(true);
    expect(checked('Events')).toBe(true);
    expect(checked('Runbooks')).toBe(true);
    expect(checked('Memory')).toBe(true);
    expect(checked('Incidents')).toBe(false);
    expect(checked('Proposals')).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: /Save/ }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const form = onSave.mock.calls[0][0];
    expect(form.tools_enabled).toBe(true);
    expect(form.builtin_tools).toEqual(['events', 'runbooks', 'memory']);
  });

  it('sends the toggled values in display order', async () => {
    const onSave = renderForm();
    fireEvent.change(screen.getByPlaceholderText(/Morning incident digest/), { target: { value: 'Job' } });
    fireEvent.change(screen.getByPlaceholderText(/What should the cron-agent do/), { target: { value: 'Do it' } });

    fireEvent.click(screen.getByLabelText('Allow connector tools'));
    fireEvent.click(screen.getByLabelText('Proposals'));
    fireEvent.click(screen.getByLabelText('Runbooks'));
    fireEvent.click(screen.getByLabelText('Incidents'));

    fireEvent.click(screen.getByRole('button', { name: /Save/ }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const form = onSave.mock.calls[0][0];
    expect(form.tools_enabled).toBe(false);
    expect(form.builtin_tools).toEqual(['events', 'memory', 'incidents', 'proposals']);
  });

  it('loads the values of an existing job and can clear the list', async () => {
    const onSave = renderForm({ isCreating: false, initial: job });
    expect(checked('Allow connector tools')).toBe(false);
    expect(checked('Incidents')).toBe(true);
    expect(checked('Events')).toBe(false);

    fireEvent.click(screen.getByLabelText('Incidents'));
    fireEvent.click(screen.getByRole('button', { name: /Save/ }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].builtin_tools).toEqual([]);
    expect(onSave.mock.calls[0][0].tools_enabled).toBe(false);
  });
});
