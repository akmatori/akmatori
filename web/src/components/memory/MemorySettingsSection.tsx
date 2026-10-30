import { useSearchParams } from 'react-router-dom';
import MemoryList from './MemoryList';
import MemoryDetailView from './MemoryDetailView';

// MemorySettingsSection is Settings → Memory. `?memory=<id>` opens one
// memory; `?incident=<uuid>` lists the memories from one incident.
export default function MemorySettingsSection() {
  const [params] = useSearchParams();
  const memoryId = Number(params.get('memory'));
  if (Number.isInteger(memoryId) && memoryId > 0) {
    // key resets the view's state when a link opens another memory.
    return <MemoryDetailView key={memoryId} id={memoryId} />;
  }
  return <MemoryList incidentUuid={params.get('incident') ?? ''} />;
}
