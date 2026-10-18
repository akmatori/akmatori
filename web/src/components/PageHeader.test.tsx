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

  it('keeps the title as a screen-reader h1, since the top bar shows the page name', () => {
    render(<PageHeader title="Title" />);
    const h1 = screen.getByRole('heading', { level: 1, name: 'Title' });
    expect(h1.className).toContain('sr-only');
  });

  it('pushes actions to the end of the row', () => {
    render(<PageHeader title="Title" action={<button>Act</button>} />);
    const wrapper = screen.getByRole('button', { name: 'Act' }).parentElement as HTMLElement;
    expect(wrapper.className).toContain('ml-auto');
  });
});
