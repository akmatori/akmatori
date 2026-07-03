import type { Proposal } from '../../types';
import {
  kindConfig,
  fieldOrder,
  fieldLabel,
  parseContent,
  fieldToText,
} from './proposalHelpers';

interface ProposalDiffProps {
  proposal: Proposal;
}

// ProposalDiff renders the per-kind fields as side-by-side "Current" /
// "Proposed" text blocks. For *_new kinds only the proposed column is shown.
export default function ProposalDiff({ proposal }: ProposalDiffProps) {
  const kc = kindConfig(proposal.kind);
  const current = parseContent(proposal.current_snapshot);
  const proposed = parseContent(proposal.proposed_content);
  const fields = fieldOrder(proposal.kind);

  return (
    <div className="space-y-4">
      {fields.map((field) => {
        const currentText = fieldToText(current[field]);
        const proposedText = fieldToText(proposed[field]);
        const changed = kc.isUpdate && currentText !== proposedText;
        // Skip fields that are empty on both sides.
        if (!currentText && !proposedText) return null;
        return (
          <div key={field}>
            <div className="flex items-center gap-2 mb-1.5">
              <span className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {fieldLabel(field)}
              </span>
              {changed && (
                <span className="badge badge-warning text-xs">changed</span>
              )}
            </div>
            {kc.isUpdate ? (
              <div className="grid grid-cols-2 gap-3">
                <DiffCell label="Current" text={currentText} tone={changed ? 'removed' : 'neutral'} />
                <DiffCell label="Proposed" text={proposedText} tone={changed ? 'added' : 'neutral'} />
              </div>
            ) : (
              <DiffCell label="Proposed" text={proposedText} tone="added" />
            )}
          </div>
        );
      })}
    </div>
  );
}

function DiffCell({
  label,
  text,
  tone,
}: {
  label: string;
  text: string;
  tone: 'added' | 'removed' | 'neutral';
}) {
  const toneClasses =
    tone === 'added'
      ? 'bg-green-50 dark:bg-green-900/10 border-green-200 dark:border-green-800/50'
      : tone === 'removed'
        ? 'bg-red-50 dark:bg-red-900/10 border-red-200 dark:border-red-800/50'
        : 'bg-gray-50 dark:bg-gray-900/30 border-gray-200 dark:border-gray-700';
  return (
    <div>
      <div className="text-xs text-gray-400 dark:text-gray-500 mb-1">{label}</div>
      <pre
        className={`whitespace-pre-wrap break-words font-mono text-xs rounded-lg border p-3 max-h-72 overflow-y-auto text-gray-800 dark:text-gray-200 ${toneClasses}`}
      >
        {text || '(empty)'}
      </pre>
    </div>
  );
}
