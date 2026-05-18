import type { Channel, MessagingProvider } from '../../types';

// providerLabel returns a short, user-facing label for a messaging provider.
export function providerLabel(provider: MessagingProvider | string): string {
  switch (provider) {
    case 'slack':
      return 'Slack';
    case 'telegram':
      return 'Telegram';
    default:
      return String(provider);
  }
}

// providerIconText returns the two-letter chip text rendered as a provider icon
// inside the Channels table and the ChannelPicker. Keeping this in a helper
// avoids drift between the two surfaces.
export function providerIconText(provider: MessagingProvider | string): string {
  switch (provider) {
    case 'slack':
      return 'SL';
    case 'telegram':
      return 'TG';
    default:
      return String(provider).slice(0, 2).toUpperCase();
  }
}

// channelDisplayLabel returns the text shown in the ChannelPicker dropdown.
// Falls back to external_id when no display name has been set so the picker
// is always selectable.
export function channelDisplayLabel(ch: Channel): string {
  const name = ch.display_name?.trim();
  if (name) return name;
  return ch.external_id;
}

// filterPostableChannels returns channels that can be used as outbound post
// destinations. The ChannelPicker uses this — listener-only channels are
// hidden so operators can't bind an alert source to a channel that can't
// actually receive messages.
export function filterPostableChannels(channels: Channel[]): Channel[] {
  return channels.filter((c) => c.can_post && c.enabled);
}

// pickerOption is the rendered shape of one entry in the ChannelPicker drop
// down. The picker keeps its own React structure but constructs these from
// the helper to keep filter / sort behaviour testable as a pure function.
export interface ChannelPickerOption {
  uuid: string;
  label: string;
  provider: MessagingProvider | string;
  isDefault: boolean;
  icon: string;
}

// buildChannelPickerOptions sorts post-capable channels by provider then
// display label, surfacing the per-provider default first within its group.
// The output is purely data — rendering happens in ChannelPicker.tsx.
export function buildChannelPickerOptions(channels: Channel[]): ChannelPickerOption[] {
  const postable = filterPostableChannels(channels);
  const sorted = [...postable].sort((a, b) => {
    const ap = a.integration?.provider ?? '';
    const bp = b.integration?.provider ?? '';
    if (ap !== bp) return ap.localeCompare(bp);
    if (a.is_default_post !== b.is_default_post) return a.is_default_post ? -1 : 1;
    return channelDisplayLabel(a).localeCompare(channelDisplayLabel(b));
  });
  return sorted.map((c) => ({
    uuid: c.uuid,
    label: channelDisplayLabel(c),
    provider: c.integration?.provider ?? '',
    isDefault: c.is_default_post,
    icon: providerIconText(c.integration?.provider ?? ''),
  }));
}

// ChannelRole is a single capability badge rendered next to a channel in the
// ChannelsManager table.
export type ChannelRole = 'default' | 'post' | 'listen' | 'disabled';

// channelRoles returns the badges to display next to a channel row. Default
// is mutually informative with post (a default-post channel is also postable),
// so we surface "default" as the strongest signal and skip "post" when both
// would apply.
export function channelRoles(ch: Channel): ChannelRole[] {
  const roles: ChannelRole[] = [];
  if (!ch.enabled) {
    roles.push('disabled');
  }
  if (ch.is_default_post) {
    roles.push('default');
  } else if (ch.can_post) {
    roles.push('post');
  }
  if (ch.can_listen) {
    roles.push('listen');
  }
  return roles;
}

// roleBadgeClass returns the Tailwind class for a role chip. Kept in this
// helper so styling stays consistent across the manager table and any future
// surface that lists channels.
export function roleBadgeClass(role: ChannelRole): string {
  switch (role) {
    case 'default':
      return 'badge badge-primary';
    case 'post':
      return 'badge badge-success';
    case 'listen':
      return 'badge badge-default';
    case 'disabled':
      return 'badge badge-warning';
  }
}

// roleBadgeLabel returns the human-friendly label for a role chip.
export function roleBadgeLabel(role: ChannelRole): string {
  switch (role) {
    case 'default':
      return 'Default post';
    case 'post':
      return 'Post';
    case 'listen':
      return 'Listen';
    case 'disabled':
      return 'Disabled';
  }
}
