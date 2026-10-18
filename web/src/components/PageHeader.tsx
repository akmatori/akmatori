import type { ReactNode } from 'react';

interface PageHeaderProps {
  title: string;
  description?: string;
  action?: ReactNode;
}

// PageHeader is the strip above a page's content: what the page is for and
// its actions. The top bar already shows the page name, so the h1 is for
// screen readers and document structure only.
export default function PageHeader({ title, description, action }: PageHeaderProps) {
  return (
    <div className="mb-5 flex flex-wrap items-center gap-x-6 gap-y-3">
      <h1 className="sr-only">{title}</h1>
      {description && <p className="flex-[1_1_320px] max-w-[72ch] text-sm text-fg-muted">{description}</p>}
      {action && <div className="ml-auto flex flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}
