import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { Activity } from 'lucide-react';
import EmptyState from './EmptyState';
import Pagination from './Pagination';
import SegmentedControl from './SegmentedControl';
import SourceKindChip from './SourceKindChip';

describe('EmptyState', () => {
  it('renders title, optional description and icon', () => {
    const { container } = render(<EmptyState icon={Activity} title="Nothing here" description="Try later" />);
    expect(screen.getByText('Nothing here')).toBeTruthy();
    expect(screen.getByText('Try later')).toBeTruthy();
    expect(container.querySelector('svg')).toBeTruthy();
  });

  it('omits description and icon when not provided', () => {
    const { container } = render(<EmptyState title="Empty" />);
    expect(container.querySelectorAll('p').length).toBe(1);
    expect(container.querySelector('svg')).toBeNull();
  });
});

describe('Pagination', () => {
  it('renders nothing for a single page', () => {
    const { container } = render(
      <Pagination page={1} totalPages={1} totalItems={3} onPageChange={() => {}} />
    );
    expect(container.firstChild).toBeNull();
  });

  it('disables prev on the first page and next on the last', () => {
    const onPageChange = vi.fn();
    const { rerender } = render(
      <Pagination page={1} totalPages={3} totalItems={120} onPageChange={onPageChange} />
    );
    expect(screen.getByText(/Page 1 of 3/)).toBeTruthy();
    expect((screen.getByLabelText('Previous page') as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByLabelText('Next page'));
    expect(onPageChange).toHaveBeenCalledWith(2);

    rerender(<Pagination page={3} totalPages={3} totalItems={120} onPageChange={onPageChange} />);
    expect((screen.getByLabelText('Next page') as HTMLButtonElement).disabled).toBe(true);
  });

  it('shows a per-page selector only when a handler is given', () => {
    const onPerPageChange = vi.fn();
    const { rerender } = render(
      <Pagination page={1} totalPages={2} totalItems={60} onPageChange={() => {}} />
    );
    expect(screen.queryByRole('combobox')).toBeNull();

    rerender(
      <Pagination
        page={1}
        totalPages={2}
        totalItems={60}
        onPageChange={() => {}}
        perPage={50}
        onPerPageChange={onPerPageChange}
      />
    );
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '100' } });
    expect(onPerPageChange).toHaveBeenCalledWith(100);
  });
});

describe('SegmentedControl', () => {
  it('highlights the active option and reports clicks', () => {
    const onChange = vi.fn();
    render(
      <SegmentedControl
        value="open"
        options={[{ value: 'open', label: 'Open' }, { value: 'history', label: 'History' }]}
        onChange={onChange}
      />
    );
    expect(screen.getByText('Open').className).toContain('bg-primary-500');
    expect(screen.getByText('History').className).not.toContain('bg-primary-500');
    fireEvent.click(screen.getByText('History'));
    expect(onChange).toHaveBeenCalledWith('history');
  });
});

describe('SourceKindChip', () => {
  it('maps known kinds and falls back to Manual', () => {
    const { rerender } = render(<SourceKindChip kind="alert" />);
    expect(screen.getByText('Alert')).toBeTruthy();
    rerender(<SourceKindChip kind="slack_mention" />);
    expect(screen.getByText('Slack')).toBeTruthy();
    rerender(<SourceKindChip kind="cron" />);
    expect(screen.getByText('Cron')).toBeTruthy();
    rerender(<SourceKindChip />);
    expect(screen.getByText('Manual')).toBeTruthy();
    rerender(<SourceKindChip kind="something" />);
    expect(screen.getByText('Manual')).toBeTruthy();
  });
});
