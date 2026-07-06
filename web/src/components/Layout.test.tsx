import { describe, it, expect, vi, beforeAll, afterEach, afterAll } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Layout from './Layout';

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  });
});

vi.mock('../context/AuthContext', () => ({
  useAuth: () => ({ user: { username: 'tester' }, logout: vi.fn() }),
}));

vi.mock('../context/ThemeContext', () => ({
  useTheme: () => ({ theme: 'light', setTheme: vi.fn() }),
}));

vi.mock('../hooks/useSetupStatus', () => ({
  useSetupStatus: () => ({ showOnboarding: false, dismissOnboarding: vi.fn(), markComplete: vi.fn() }),
}));

vi.mock('./OnboardingWizard', () => ({ default: () => null }));

vi.mock('../api/client', () => ({
  proposalsApi: { pendingCount: () => Promise.resolve({ pending: 0 }) },
}));

function renderLayout() {
  return render(
    <MemoryRouter>
      <Layout><div>content</div></Layout>
    </MemoryRouter>
  );
}

describe('Layout mobile sidebar', () => {
  afterEach(() => {
    document.body.classList.remove('overflow-hidden');
  });

  it('hamburger button has type=button', () => {
    renderLayout();
    const hamburger = screen.getByLabelText('Open menu');
    expect(hamburger.getAttribute('type')).toBe('button');
  });

  it('does not focus hamburger button on initial mount', () => {
    renderLayout();
    const hamburger = screen.getByLabelText('Open menu');
    expect(document.activeElement).not.toBe(hamburger);
  });

  it('opening drawer adds overflow-hidden to body', () => {
    renderLayout();
    expect(document.body.classList.contains('overflow-hidden')).toBe(false);
    fireEvent.click(screen.getByLabelText('Open menu'));
    expect(document.body.classList.contains('overflow-hidden')).toBe(true);
  });

  it('backdrop is absent by default', () => {
    renderLayout();
    expect(document.querySelector('.fixed.inset-0.z-30')).toBeNull();
  });

  it('main content is inert when drawer is open and interactive when closed', () => {
    renderLayout();
    const main = document.querySelector('main')!;
    expect(main.hasAttribute('inert')).toBe(false);
    fireEvent.click(screen.getByLabelText('Open menu'));
    expect(main.hasAttribute('inert')).toBe(true);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(main.hasAttribute('inert')).toBe(false);
  });

  it('Escape key closes drawer and removes body scroll lock', () => {
    renderLayout();
    fireEvent.click(screen.getByLabelText('Open menu'));
    expect(document.body.classList.contains('overflow-hidden')).toBe(true);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(document.body.classList.contains('overflow-hidden')).toBe(false);
  });

  it('nav link click closes drawer', () => {
    renderLayout();
    fireEvent.click(screen.getByLabelText('Open menu'));
    expect(document.body.classList.contains('overflow-hidden')).toBe(true);
    const links = screen.getAllByRole('link');
    fireEvent.click(links[0]);
    expect(document.body.classList.contains('overflow-hidden')).toBe(false);
  });

  it('sidebar always renders full width on mobile (collapsed state does not shrink)', () => {
    renderLayout();
    // Collapse the sidebar first to verify mobile ignores that state
    const collapseBtn = screen.getByTitle('Collapse sidebar');
    fireEvent.click(collapseBtn);
    const aside = document.querySelector('aside');
    expect(aside?.className).toContain('w-64');
    // md:w-16 is present in the class string but only applies at ≥768px — on mobile the sidebar stays w-64
    expect(aside?.className).not.toMatch(/(?<![:\w])w-16(?!\w)/);
  });

  it('nav labels visible in mobile drawer even when sidebar was collapsed on desktop', () => {
    renderLayout();
    // Collapse the sidebar (button is always in DOM in jsdom regardless of CSS hidden class)
    const collapseBtn = screen.getByTitle('Collapse sidebar');
    fireEvent.click(collapseBtn);
    // Open the mobile drawer
    fireEvent.click(screen.getByLabelText('Open menu'));
    // Nav item labels must be visible in the drawer — query within <nav> to avoid matching the <h2> header
    const nav = document.querySelector('nav');
    expect(nav?.textContent).toContain('Dashboard');
    expect(nav?.textContent).toContain('Incidents');
  });

  it('theme toggle is present in the sidebar (accessible on mobile)', () => {
    renderLayout();
    const themeBtn = screen.getByTitle(/switch to/i);
    expect(themeBtn).toBeTruthy();
  });

  describe('mobile viewport (max-width: 767px)', () => {
    const desktopMock = (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    const mobileMock = (query: string) => ({
      matches: query === '(max-width: 767px)',
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });

    beforeAll(() => {
      Object.defineProperty(window, 'matchMedia', { writable: true, value: mobileMock });
    });
    afterAll(() => {
      Object.defineProperty(window, 'matchMedia', { writable: true, value: desktopMock });
    });

    it('aside is inert when mobile drawer is closed and not inert when open', () => {
      renderLayout();
      const aside = document.querySelector('aside')!;

      // Drawer is closed on mobile — aside must be inert to prevent keyboard tab-into
      expect(aside.hasAttribute('inert')).toBe(true);

      // Opening the drawer removes inert so keyboard users can reach nav items
      fireEvent.click(screen.getByLabelText('Open menu'));
      expect(aside.hasAttribute('inert')).toBe(false);
    });
  });
});
