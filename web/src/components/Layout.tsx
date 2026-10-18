import { useState, useEffect, useRef, useCallback } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ChevronLeft, Menu, Sun, Moon, LogOut } from 'lucide-react';
import { useAuth } from '../context/useAuth';
import { useTheme } from '../context/useTheme';
import { useSetupStatus } from '../hooks/useSetupStatus';
import { useSystemStatus } from '../hooks/useSystemStatus';
import { useInterval } from '../hooks/useInterval';
import { useLatestRef } from '../hooks/useLatestRef';
import OnboardingWizard from './OnboardingWizard';
import { proposalsApi } from '../api/client';
import { NAV_GROUPS, SETTINGS_NAV_ITEM, findNavItem, type NavItem } from './layout/navigation';

interface LayoutProps {
  children: React.ReactNode;
}

// usePendingProposalsCount polls the pending-proposals count for the nav
// badge. Failures render nothing (count 0) — the badge is decorative and
// must never break navigation.
function usePendingProposalsCount(): number {
  const [count, setCount] = useState(0);
  const load = useCallback(async () => {
    try {
      const res = await proposalsApi.pendingCount();
      setCount(res.pending);
    } catch {
      setCount(0);
    }
  }, []);
  const loadRef = useLatestRef(load);
  useEffect(() => {
    void loadRef.current();
  }, [loadRef]);
  useInterval(() => { void load(); }, 60_000);
  return count;
}

function StatusDot({ tone }: { tone: 'ok' | 'need' | 'fail' }) {
  const color = tone === 'ok' ? 'bg-state-ok' : tone === 'need' ? 'bg-state-need' : 'bg-state-fail';
  return <span aria-hidden="true" className={`w-[7px] h-[7px] rounded-full ${color}`} />;
}

function SystemStatusIndicator() {
  const { apiAvailable, llm } = useSystemStatus();
  return (
    <div className="hidden sm:flex items-center gap-4 text-[13px] text-fg-muted">
      <span className="inline-flex items-center gap-1.5">
        <StatusDot tone={apiAvailable ? 'ok' : 'fail'} />
        {apiAvailable ? 'API connected' : 'API unreachable'}
      </span>
      {llm && (llm.configured ? (
        <span className="inline-flex items-center gap-1.5" title="Active LLM model">
          <StatusDot tone="ok" />
          <span className="font-mono text-xs">{llm.model || 'LLM ready'}</span>
        </span>
      ) : (
        <Link to="/settings/ai" className="inline-flex items-center gap-1.5 text-state-need hover:underline">
          <StatusDot tone="need" />
          LLM not configured
        </Link>
      ))}
    </div>
  );
}

export default function Layout({ children }: LayoutProps) {
  const location = useLocation();
  const { user, logout } = useAuth();
  const { theme, setTheme } = useTheme();
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [isMobile, setIsMobile] = useState(() => window.matchMedia('(max-width: 767px)').matches);
  const sidebarRef = useRef<HTMLElement>(null);
  const hamburgerRef = useRef<HTMLButtonElement>(null);
  const drawerWasOpen = useRef(false);
  const { showOnboarding, dismissOnboarding, markComplete } = useSetupStatus();
  const pendingProposals = usePendingProposalsCount();
  const activeItem = findNavItem(location.pathname);
  const showLabels = !collapsed || mobileOpen;
  const isDark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);

  // Close the mobile drawer on navigation. Compared during render against the
  // last seen pathname so the close happens in the same render as the route
  // change instead of one effect later.
  const [lastPathname, setLastPathname] = useState(location.pathname);
  if (location.pathname !== lastPathname) {
    setLastPathname(location.pathname);
    setMobileOpen(false);
  }

  useEffect(() => {
    document.body.classList.toggle('overflow-hidden', mobileOpen);
    return () => { document.body.classList.remove('overflow-hidden'); };
  }, [mobileOpen]);

  useEffect(() => {
    if (!mobileOpen) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMobileOpen(false); };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [mobileOpen]);

  // Move focus into the sidebar when the mobile drawer opens so keyboard/AT
  // users are not stranded after <main inert> disables the hamburger button.
  // When the drawer closes, return focus to the hamburger button.
  // Guard with drawerWasOpen so we don't steal focus on initial mount.
  useEffect(() => {
    if (mobileOpen) {
      drawerWasOpen.current = true;
      const id = requestAnimationFrame(() => {
        const first = sidebarRef.current?.querySelector<HTMLElement>('a[href],button:not([disabled])');
        first?.focus();
      });
      return () => cancelAnimationFrame(id);
    } else if (drawerWasOpen.current) {
      drawerWasOpen.current = false;
      hamburgerRef.current?.focus();
    }
  }, [mobileOpen]);

  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)');
    const onChange = (e: MediaQueryListEvent) => {
      setIsMobile(e.matches);
      if (!e.matches) setMobileOpen(false);
    };
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);

  const renderNavLink = (item: NavItem) => {
    const Icon = item.icon;
    const isActive = activeItem?.href === item.href;
    const badge = item.href === '/proposals' && pendingProposals > 0 ? pendingProposals : 0;
    return (
      <Link
        key={item.href}
        to={item.href}
        onClick={() => setMobileOpen(false)}
        aria-current={isActive ? 'page' : undefined}
        title={showLabels ? undefined : item.name}
        className={`relative flex items-center gap-2.5 h-9 px-2.5 rounded-md text-sm transition-colors duration-100 ${
          isActive
            ? 'bg-surface-2 text-fg shadow-[inset_2px_0_0_var(--color-primary-500)]'
            : 'text-fg-muted hover:bg-surface-2 hover:text-fg'
        }`}
      >
        <Icon size={16} className="flex-shrink-0" aria-hidden="true" />
        {showLabels && <span className="flex-1 truncate">{item.name}</span>}
        {badge > 0 && (showLabels ? (
          <span className="min-w-5 h-5 px-1.5 inline-flex items-center justify-center rounded-full text-xs bg-state-need/15 text-state-need tabular-nums">
            {badge}
          </span>
        ) : (
          <span aria-label={`${badge} pending`} className="absolute top-1.5 right-1.5 w-1.5 h-1.5 rounded-full bg-state-need" />
        ))}
      </Link>
    );
  };

  return (
    <>
      {showOnboarding && (
        <OnboardingWizard
          onComplete={markComplete}
          onSkip={dismissOnboarding}
        />
      )}

      <div className="flex h-dvh bg-canvas">
        {/* Mobile backdrop */}
        {mobileOpen && (
          <div
            className="fixed inset-0 z-30 bg-black/50 md:hidden"
            onClick={() => setMobileOpen(false)}
          />
        )}

        <aside
          ref={sidebarRef}
          id="mobile-sidebar"
          inert={isMobile && !mobileOpen ? true : undefined}
          className={`
            flex flex-col border-r border-line bg-surface dark:bg-[#15191f]
            transition-[width,transform] duration-200 ease-in-out
            fixed inset-y-0 left-0 z-40 md:static md:inset-auto
            ${mobileOpen ? 'translate-x-0' : '-translate-x-full'} md:translate-x-0
            ${collapsed ? 'w-60 md:w-14' : 'w-60'}
          `}
        >
          <div className="flex items-center gap-2.5 h-14 px-3.5 flex-shrink-0">
            <img src="/akmatori.svg" alt="" className="w-7 h-7 flex-shrink-0" />
            {showLabels && <span className="font-semibold text-[15px] text-fg">Akmatori</span>}
          </div>

          <nav aria-label="Main" className="flex-1 px-2 pb-3 overflow-y-auto flex flex-col gap-4">
            {NAV_GROUPS.map((group) => (
              <div key={group.label} className="flex flex-col gap-0.5">
                {showLabels ? (
                  <span className="px-2.5 pb-1 text-xs text-fg-subtle">{group.label}</span>
                ) : (
                  <span aria-hidden="true" className="mx-3 mb-1 border-t border-line" />
                )}
                {group.items.map(renderNavLink)}
              </div>
            ))}
          </nav>

          <div className="px-2 py-2 border-t border-line flex flex-col gap-0.5">
            {renderNavLink(SETTINGS_NAV_ITEM)}
            <div className={`flex items-center gap-1 pt-1 ${showLabels ? 'px-2.5' : 'flex-col'}`}>
              {user && showLabels && (
                <span className="flex-1 text-[13px] text-fg-subtle truncate">{user.username}</span>
              )}
              <button
                type="button"
                onClick={() => setTheme(isDark ? 'light' : 'dark')}
                className="rounded-md p-2 text-fg-subtle hover:text-fg hover:bg-surface-2"
                aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
                title={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
              >
                {isDark ? <Sun size={15} /> : <Moon size={15} />}
              </button>
              <button
                type="button"
                onClick={() => setCollapsed(!collapsed)}
                className="hidden md:inline-flex rounded-md p-2 text-fg-subtle hover:text-fg hover:bg-surface-2"
                aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
                title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              >
                {collapsed ? <Menu size={15} /> : <ChevronLeft size={15} />}
              </button>
              {user && (
                <button
                  type="button"
                  onClick={logout}
                  className="rounded-md p-2 text-fg-subtle hover:text-state-fail hover:bg-surface-2"
                  aria-label="Sign out"
                  title="Sign out"
                >
                  <LogOut size={15} />
                </button>
              )}
            </div>
          </div>
        </aside>

        <main inert={mobileOpen || undefined} className="flex-1 min-w-0 flex flex-col overflow-hidden">
          <header className="h-14 flex-shrink-0 flex items-center gap-4 px-4 md:px-7 border-b border-line bg-canvas">
            <button
              ref={hamburgerRef}
              type="button"
              className="block md:hidden p-2 -ml-2 rounded-md text-fg-muted hover:text-fg hover:bg-surface-2"
              onClick={() => setMobileOpen(true)}
              aria-label="Open menu"
              aria-expanded={mobileOpen}
              aria-controls="mobile-sidebar"
            >
              <Menu size={20} />
            </button>
            <h2 className="flex-1 min-w-0 truncate text-base font-semibold text-fg">
              {activeItem?.name ?? 'Akmatori'}
            </h2>
            <SystemStatusIndicator />
          </header>

          <div className="flex-1 overflow-auto">
            <div className="p-4 sm:p-7 max-w-[1400px] mx-auto">
              {children}
            </div>
          </div>
        </main>
      </div>
    </>
  );
}
