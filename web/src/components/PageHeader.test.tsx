import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import PageHeader from './PageHeader';

describe('PageHeader', () => {
  it('renders the title', () => {
    render(<PageHeader title="My Page" />);
    expect(screen.getByText('My Page')).toBeTruthy();
  });

  it('renders description when provided', () => {
    render(<PageHeader title="Title" description="Some description" />);
    expect(screen.getByText('Some description')).toBeTruthy();
  });

  it('omits description element when not provided', () => {
    const { container } = render(<PageHeader title="Title" />);
    expect(container.querySelector('p')).toBeNull();
  });

  it('renders action content when provided', () => {
    render(<PageHeader title="Title" action={<button>Do it</button>} />);
    expect(screen.getByRole('button', { name: 'Do it' })).toBeTruthy();
  });

  it('renders no extra wrapper around action', () => {
    const { container } = render(<PageHeader title="Title" action={<button>Act</button>} />);
    const header = container.querySelector('.mb-6 > div');
    const children = Array.from(header?.children ?? []);
    // The action button should be a direct child element at the flex row level, not wrapped in a div
    const actionChild = children[children.length - 1];
    expect(actionChild?.tagName).toBe('BUTTON');
  });
});
