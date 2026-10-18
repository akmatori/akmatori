// EventStatusBadge renders the status column of a feed row. Feed rows mix
// incident statuses (completed, monitor, …) with alert statuses (firing,
// resolved), so this is distinct from the incident status config.
export default function EventStatusBadge({ status }: { status: string }) {
  switch (status) {
    case 'completed':
      return <span className="badge badge-success">Resolved</span>;
    case 'monitor':
      return <span className="badge badge-success">Monitoring</span>;
    case 'closed':
      return <span className="badge badge-default">Closed</span>;
    case 'pending':
    case 'running':
      return <span className="badge badge-primary">Ongoing</span>;
    case 'diagnosed':
      return <span className="badge badge-purple">Ongoing</span>;
    case 'failed':
      return <span className="badge badge-error">Failed</span>;
    case 'firing':
      return <span className="badge badge-primary">Firing</span>;
    case 'resolved':
      return <span className="badge badge-success">Resolved</span>;
    default:
      return <span className="badge badge-default">{status}</span>;
  }
}
