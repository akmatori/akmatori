import { useState, createContext, useContext } from 'react';
import { Link, useLocation } from 'react-router-dom';
import {
  LayoutDashboard,
  Bot,
  Wrench,
  Settings,
  Activity,
  MessageSquare,
  FileText,
  ChevronLeft,
  Menu,
  Sun,
  Moon,
  LogOut,
} from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import { useTheme } from '../context/ThemeContext';

interface LayoutProps {
  children: React.ReactNode;
}

// Sidebar context
const SidebarContext = createContext<{ collapsed: boolean }>({ collapsed: false });
export const useSidebar = () => useContext(SidebarContext);

const navigation = [
  { name: 'Dashboard', href: '/', icon: LayoutDashboard },
  { name: 'Incidents', href: '/incidents', icon: Activity },
  { name: 'Incident Manager', href: '/incident-manager', icon: MessageSquare },
  { name: 'Skills', href: '/skills', icon: Bot },
  { name: 'Tools', href: '/tools', icon: Wrench },
  { name: 'Context Files', href: '/context', icon: FileText },
  { name: 'Settings', href: '/settings', icon: Settings },
];

export default function Layout({ children }: LayoutProps) {
  const location = useLocation();
  const { user, logout } = useAuth();
  const { theme, setTheme } = useTheme();
  const [collapsed, setCollapsed] = useState(false);

  return (
    <SidebarContext.Provider value={{ collapsed }}>
      <div className="flex h-screen bg-gray-50 dark:bg-gray-900">
          {/* Sidebar */}
          <aside
            className={`
              flex flex-col border-r border-gray-200 dark:border-gray-700
              bg-white dark:bg-gray-800 transition-all duration-200 ease-in-out
              ${collapsed ? 'w-16' : 'w-64'}
            `}
          >
            {/* Logo */}
            <div className="flex items-center h-16 px-4 border-b border-gray-200 dark:border-gray-700">
              <div className="flex items-center gap-3">
                <img
                  src="/akmatori.svg"
                  alt="Akmatori"
                  className="w-8 h-8 flex-shrink-0"
                />
                {!collapsed && (
                  <h1 className="font-semibold text-gray-900 dark:text-white animate-fade-in">
                    Akmatori
                  </h1>
                )}
              </div>
            </div>

            {/* Navigation */}
            <nav className="flex-1 p-3 space-y-1 overflow-y-auto">
              {navigation.map((item) => {
                const Icon = item.icon;
                const isActive = location.pathname === item.href;

                return (
                  <Link
                    key={item.name}
                    to={item.href}
                    className={`
                      flex items-center gap-3 px-3 py-2.5 rounded-lg
                      transition-colors duration-150
                      ${isActive
                        ? 'bg-primary-50 dark:bg-primary-900/20 text-primary-600 dark:text-primary-400'
                        : 'text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700/50'
                      }
                    `}
                    title={collapsed ? item.name : undefined}
                  >
                    <Icon size={20} className={isActive ? 'text-primary-500' : ''} />
                    {!collapsed && (
                      <span className="text-sm font-medium">{item.name}</span>
                    )}
                  </Link>
                );
              })}
            </nav>

            {/* Footer */}
            <div className="p-3 border-t border-gray-200 dark:border-gray-700 space-y-2">
              {/* User Info & Logout */}
              {user && (
                <div className={`flex ${collapsed ? 'justify-center' : 'justify-between'} items-center px-3 py-2`}>
                  {!collapsed && (
                    <span className="text-xs text-gray-500 dark:text-gray-400 truncate">
                      {user.username}
                    </span>
                  )}
                  <button
                    onClick={logout}
                    className="p-1.5 rounded-md text-gray-400 hover:text-red-500 dark:hover:text-red-400 hover:bg-gray-100 dark:hover:bg-gray-700/50 transition-colors"
                    title="Sign out"
                  >
                    <LogOut size={14} />
                  </button>
                </div>
              )}

              {/* Theme Toggle & Collapse */}
              <div className={`flex ${collapsed ? 'justify-center' : 'justify-between'} items-center px-3 py-2`}>
                {/* Dark/Light Mode Toggle */}
                <button
                  onClick={() => setTheme(theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'light' : 'dark')}
                  className="p-1.5 rounded-md text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700/50 transition-colors"
                  title={theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'Switch to light mode' : 'Switch to dark mode'}
                >
                  {theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches) ? (
                    <Sun size={16} />
                  ) : (
                    <Moon size={16} />
                  )}
                </button>

                {/* Collapse Toggle */}
                <button
                  onClick={() => setCollapsed(!collapsed)}
                  className="p-1.5 rounded-md text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700/50 transition-colors"
                  title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
                >
                  {collapsed ? <Menu size={16} /> : <ChevronLeft size={16} />}
                </button>
              </div>
            </div>
          </aside>

          {/* Main content */}
          <main className="flex-1 flex flex-col overflow-hidden">
            {/* Top bar */}
            <header className="h-16 flex items-center justify-between px-6 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
              <div>
                <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
                  {navigation.find(n => n.href === location.pathname)?.name || 'Page'}
                </h2>
              </div>
              <div className="flex items-center gap-3">
                <div className="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
                  <span className="w-2 h-2 rounded-full bg-green-500"></span>
                  <span>System Online</span>
                </div>
              </div>
            </header>

            {/* Content */}
            <div className="flex-1 overflow-auto">
              <div className="p-6 max-w-7xl mx-auto">
                {children}
              </div>
            </div>
          </main>
        </div>
      </SidebarContext.Provider>
  );
}
