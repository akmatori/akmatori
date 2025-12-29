import PageHeader from '../components/PageHeader';
import ContextFilesManager from '../components/ContextFilesManager';

export default function ContextFiles() {
  return (
    <div>
      <PageHeader
        title="Context Files"
        description="Upload and manage context files that can be referenced in skill prompts"
      />

      <ContextFilesManager />
    </div>
  );
}
