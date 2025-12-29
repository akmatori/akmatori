import { useState, useEffect } from 'react';
import { X, Save, Edit2, Eye, Loader2 } from 'lucide-react';
import type { ScriptInfo } from '../types';

interface ScriptViewerModalProps {
  isOpen: boolean;
  onClose: () => void;
  scriptInfo: ScriptInfo | null;
  loading: boolean;
  onSave: (content: string) => Promise<void>;
}

export default function ScriptViewerModal({
  isOpen,
  onClose,
  scriptInfo,
  loading,
  onSave,
}: ScriptViewerModalProps) {
  const [isEditing, setIsEditing] = useState(false);
  const [editedContent, setEditedContent] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (scriptInfo) {
      setEditedContent(scriptInfo.content);
      setIsEditing(false);
      setError('');
    }
  }, [scriptInfo]);

  useEffect(() => {
    if (!isOpen) {
      setIsEditing(false);
      setError('');
    }
  }, [isOpen]);

  const handleSave = async () => {
    try {
      setSaving(true);
      setError('');
      await onSave(editedContent);
      setIsEditing(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save script');
    } finally {
      setSaving(false);
    }
  };

  const handleCancel = () => {
    if (scriptInfo) {
      setEditedContent(scriptInfo.content);
    }
    setIsEditing(false);
    setError('');
  };

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  };

  const formatDate = (dateString: string) => {
    return new Date(dateString).toLocaleString();
  };

  // Handle escape key to close modal
  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !saving) {
        onClose();
      }
    };
    if (isOpen) {
      document.addEventListener('keydown', handleEscape);
      return () => document.removeEventListener('keydown', handleEscape);
    }
  }, [isOpen, saving, onClose]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="script-modal-title">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/50 transition-opacity"
        onClick={saving ? undefined : onClose}
      />

      {/* Modal */}
      <div className="flex min-h-full items-center justify-center p-4">
        <div className="relative w-full max-w-4xl bg-white dark:bg-gray-800 rounded-xl shadow-xl">
          {/* Header */}
          <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200 dark:border-gray-700">
            <div className="flex items-center gap-3">
              <h3 id="script-modal-title" className="text-lg font-semibold text-gray-900 dark:text-white">
                {scriptInfo?.filename || 'Loading...'}
              </h3>
              {scriptInfo && (
                <div className="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                  <span>{formatSize(scriptInfo.size)}</span>
                  <span>|</span>
                  <span>Modified: {formatDate(scriptInfo.modified_at)}</span>
                </div>
              )}
            </div>
            <button
              onClick={onClose}
              className="p-2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          {/* Content */}
          <div className="p-6">
            {loading ? (
              <div className="flex items-center justify-center py-12">
                <Loader2 className="w-8 h-8 animate-spin text-primary-500" />
              </div>
            ) : scriptInfo ? (
              <>
                {/* Mode toggle */}
                <div className="flex items-center gap-2 mb-4">
                  <button
                    onClick={() => setIsEditing(false)}
                    className={`flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg transition-colors ${
                      !isEditing
                        ? 'bg-primary-100 dark:bg-primary-900/30 text-primary-700 dark:text-primary-300'
                        : 'text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700'
                    }`}
                  >
                    <Eye className="w-4 h-4" />
                    View
                  </button>
                  <button
                    onClick={() => setIsEditing(true)}
                    className={`flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg transition-colors ${
                      isEditing
                        ? 'bg-primary-100 dark:bg-primary-900/30 text-primary-700 dark:text-primary-300'
                        : 'text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700'
                    }`}
                  >
                    <Edit2 className="w-4 h-4" />
                    Edit
                  </button>
                </div>

                {/* Error message */}
                {error && (
                  <div className="mb-4 p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg text-red-700 dark:text-red-300 text-sm">
                    {error}
                  </div>
                )}

                {/* Code display/editor */}
                {isEditing ? (
                  <textarea
                    value={editedContent}
                    onChange={(e) => setEditedContent(e.target.value)}
                    className="w-full h-96 p-4 font-mono text-sm bg-gray-900 text-gray-100 rounded-lg border border-gray-700 focus:ring-2 focus:ring-primary-500 focus:border-transparent resize-y"
                    spellCheck={false}
                  />
                ) : (
                  <pre className="w-full h-96 p-4 font-mono text-sm bg-gray-900 text-gray-100 rounded-lg overflow-auto">
                    <code>{scriptInfo.content}</code>
                  </pre>
                )}
              </>
            ) : (
              <div className="text-center py-12 text-gray-500 dark:text-gray-400">
                No script selected
              </div>
            )}
          </div>

          {/* Footer */}
          {scriptInfo && (
            <div className="flex items-center justify-end gap-3 px-6 py-4 border-t border-gray-200 dark:border-gray-700">
              {isEditing ? (
                <>
                  <button
                    onClick={handleCancel}
                    disabled={saving}
                    className="px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 bg-gray-100 dark:bg-gray-700 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-600 transition-colors disabled:opacity-50"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleSave}
                    disabled={saving}
                    className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-white bg-primary-600 rounded-lg hover:bg-primary-700 transition-colors disabled:opacity-50"
                  >
                    {saving ? (
                      <Loader2 className="w-4 h-4 animate-spin" />
                    ) : (
                      <Save className="w-4 h-4" />
                    )}
                    Save Changes
                  </button>
                </>
              ) : (
                <button
                  onClick={onClose}
                  className="px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 bg-gray-100 dark:bg-gray-700 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-600 transition-colors"
                >
                  Close
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
