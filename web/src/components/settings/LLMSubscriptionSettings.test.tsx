import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import LLMSettingsSection from './LLMSettingsSection';
import { llmSettingsApi } from '../../api/client';

vi.mock('../../api/client', () => ({
  llmSettingsApi: { list: vi.fn(), create: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(llmSettingsApi.list).mockResolvedValue({ configs: [], active_id: 0 });
});
afterEach(cleanup);

describe('subscription configuration', () => {
  it('creates a subscription without asking for an API key', async () => {
    const { container } = render(<LLMSettingsSection />);
    fireEvent.click(await screen.findByRole('button', { name: /Add Configuration/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Codex (subscription)' }));
    expect(container.querySelector('input[type="password"]')).toBeNull();
    expect(screen.getByText(/Saving this configuration does not verify/)).toBeTruthy();
    const name = container.querySelector('input[maxlength="100"]') as HTMLInputElement;
    fireEvent.change(name, { target: { value: 'My subscription' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(llmSettingsApi.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'My subscription', provider: 'openai-codex', api_key: undefined, base_url: undefined,
    })));
  });

  it('keeps API-key creation disabled when its key is missing', async () => {
    const { container } = render(<LLMSettingsSection />);
    fireEvent.click(await screen.findByRole('button', { name: /Add Configuration/ }));
    const name = container.querySelector('input[maxlength="100"]') as HTMLInputElement;
    fireEvent.change(name, { target: { value: 'API configuration' } });
    expect(container.querySelector('input[type="password"]')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Create' }) as HTMLButtonElement).disabled).toBe(true);
  });
});
