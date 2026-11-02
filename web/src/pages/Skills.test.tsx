import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Skill } from '../types';

const api = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  delete: vi.fn(),
  sync: vi.fn(),
}));
const toolsList = vi.hoisted(() => vi.fn());

vi.mock('../api/client', () => ({
  skillsApi: api,
  toolsApi: { list: toolsList },
}));

import Skills from './Skills';

const skill = (over: Partial<Skill>): Skill => ({
  id: 1,
  name: 'zabbix-analyst',
  description: 'Looks at Zabbix',
  category: '',
  prompt: 'You analyse Zabbix.',
  is_system: false,
  enabled: true,
  created_at: '',
  updated_at: '',
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  api.list.mockResolvedValue([
    skill({ id: 1, name: 'connected-tools', description: 'Tools', prompt: '# Tools\n- zabbix.get', is_system: true }),
    skill({ id: 2, name: 'incident-manager', description: 'Core', prompt: 'core', is_system: true }),
    skill({ id: 3 }),
  ]);
});

function renderPage() {
  render(
    <MemoryRouter>
      <Skills />
    </MemoryRouter>,
  );
}

describe('Skills page', () => {
  it('loads skills without calling the tools API', async () => {
    renderPage();
    expect(await screen.findByText('zabbix-analyst')).toBeTruthy();
    expect(toolsList).not.toHaveBeenCalled();
  });

  it('shows connected-tools as a generated read-only system skill', async () => {
    renderPage();
    const name = await screen.findByText('connected-tools');
    expect(screen.getByText('Generated')).toBeTruthy();
    fireEvent.click(name);
    expect(screen.getByText('View: connected-tools')).toBeTruthy();
    expect(screen.getByText('System Skill (Read-Only)')).toBeTruthy();
    expect(screen.getAllByText(/Generated from your enabled connectors/).length).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: /Save/ })).toBeNull();
    expect(screen.queryByPlaceholderText(/Define the skill's role/)).toBeNull();
  });

  it('edits a normal skill without a tool picker and shows the Connectors hint', async () => {
    renderPage();
    fireEvent.click(await screen.findByText('zabbix-analyst'));
    expect(screen.getByText('Edit: zabbix-analyst')).toBeTruthy();
    expect(screen.queryByText('Tool Connections')).toBeNull();
    // One in the page header, one in the modal where the tool picker was.
    expect(screen.getAllByText(/are available to every skill/)).toHaveLength(2);
    const link = screen.getAllByRole('link', { name: 'Connectors' })[0];
    expect(link.getAttribute('href')).toBe('/connectors');
  });

  it('saves a skill without calling the removed tools endpoint', async () => {
    api.update.mockResolvedValue(skill({}));
    renderPage();
    fireEvent.click(await screen.findByText('zabbix-analyst'));
    fireEvent.click(screen.getByRole('button', { name: /Save/ }));
    await vi.waitFor(() => expect(api.update).toHaveBeenCalled());
    expect(api.update.mock.calls[0][0]).toBe('zabbix-analyst');
    expect(Object.keys(api).sort()).toEqual(['create', 'delete', 'list', 'sync', 'update']);
  });
});
