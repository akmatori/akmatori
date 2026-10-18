import { describe, it, expect } from 'vitest';
import { findNavItem } from './navigation';

describe('findNavItem', () => {
  it('matches the root only on "/"', () => {
    expect(findNavItem('/')?.name).toBe('Overview');
    expect(findNavItem('/unknown')).toBeUndefined();
  });

  it('maps detail routes to their section', () => {
    expect(findNavItem('/incidents/abc-123')?.name).toBe('Incidents');
    expect(findNavItem('/proposals/xyz')?.name).toBe('Proposals');
    expect(findNavItem('/settings/ai')?.name).toBe('Settings');
  });

  it('does not match on a shared prefix without a slash', () => {
    expect(findNavItem('/incidentsfoo')).toBeUndefined();
  });

  it('labels the feed route as Events', () => {
    expect(findNavItem('/feed')?.name).toBe('Events');
  });
});
