import {
  LayoutDashboard,
  Activity,
  Rss,
  Lightbulb,
  BookOpen,
  FileText,
  Bot,
  Plug,
  Clock,
  Settings,
  type LucideIcon,
} from 'lucide-react';

export interface NavItem {
  name: string;
  href: string;
  icon: LucideIcon;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

// Daily on-call work first, then what the agent knows, then what runs on its
// own. Settings sits apart in the sidebar footer.
export const NAV_GROUPS: NavGroup[] = [
  {
    label: 'Operate',
    items: [
      { name: 'Overview', href: '/', icon: LayoutDashboard },
      { name: 'Incidents', href: '/incidents', icon: Activity },
      { name: 'Events', href: '/events', icon: Rss },
      { name: 'Proposals', href: '/proposals', icon: Lightbulb },
    ],
  },
  {
    label: 'Knowledge',
    items: [
      { name: 'Runbooks', href: '/runbooks', icon: BookOpen },
      { name: 'Context files', href: '/context', icon: FileText },
      { name: 'Skills', href: '/skills', icon: Bot },
    ],
  },
  {
    label: 'Automate',
    items: [
      { name: 'Connectors', href: '/connectors', icon: Plug },
      { name: 'Cron jobs', href: '/cron', icon: Clock },
    ],
  },
];

export const SETTINGS_NAV_ITEM: NavItem = { name: 'Settings', href: '/settings', icon: Settings };

const ALL_ITEMS: NavItem[] = [...NAV_GROUPS.flatMap((g) => g.items), SETTINGS_NAV_ITEM];

// findNavItem returns the nav entry that owns a pathname. Detail routes
// (/incidents/:uuid, /settings/ai) belong to their section; "/" matches only
// itself so it does not swallow every route.
export function findNavItem(pathname: string): NavItem | undefined {
  return ALL_ITEMS.find((item) =>
    item.href === '/'
      ? pathname === '/'
      : pathname === item.href || pathname.startsWith(`${item.href}/`),
  );
}
