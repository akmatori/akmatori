import type { ReactNode } from 'react';
import { Navigate, NavLink, useParams } from 'react-router-dom';
import type { LucideIcon } from 'lucide-react';

export interface PageSectionDef {
  id: string;
  title: string;
  description: string;
  icon: LucideIcon;
  render: () => ReactNode;
}

interface SectionedPageProps {
  // Route prefix without a trailing slash, e.g. "/settings".
  basePath: string;
  // Accessible name of the section list, e.g. "Settings".
  label: string;
  sections: PageSectionDef[];
}

// SectionedPage is a page with a list of sections on the left and the active
// section on the right. Each section has its own URL (<basePath>/<id>) so it
// can be linked to directly; an unknown or missing id goes to the first one.
export default function SectionedPage({ basePath, label, sections }: SectionedPageProps) {
  const { section } = useParams<{ section?: string }>();
  const active = sections.find((s) => s.id === section);
  if (!active) return <Navigate to={`${basePath}/${sections[0].id}`} replace />;

  const titleId = `${label.toLowerCase().replace(/\s+/g, '-')}-title`;

  return (
    <div className="flex flex-col md:flex-row gap-6 md:gap-10">
      <nav aria-label={label} className="md:w-52 flex-shrink-0">
        <ul className="flex md:flex-col gap-0.5 overflow-x-auto md:overflow-visible -mx-1 px-1 pb-1 md:pb-0">
          {sections.map(({ id, title, icon: Icon }) => (
            <li key={id} className="flex-shrink-0">
              <NavLink
                to={`${basePath}/${id}`}
                className={({ isActive }) =>
                  `flex items-center gap-2.5 h-9 px-2.5 rounded-md text-sm whitespace-nowrap transition-colors ${
                    isActive ? 'bg-surface-2 text-fg' : 'text-fg-muted hover:bg-surface-2 hover:text-fg'
                  }`
                }
              >
                <Icon size={15} aria-hidden="true" />
                {title}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      <section aria-labelledby={titleId} className="flex-1 min-w-0 max-w-4xl">
        <header className="mb-6">
          <h1 id={titleId} className="text-xl font-semibold text-fg">{active.title}</h1>
          <p className="mt-1 text-fg-muted">{active.description}</p>
        </header>
        {active.render()}
      </section>
    </div>
  );
}
