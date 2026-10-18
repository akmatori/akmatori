import { useState, useEffect } from 'react';
import { Plus, Pencil, Trash2, BookOpen, X } from 'lucide-react';
import { runbooksApi } from '../api/client';
import type { Runbook } from '../types';
import PageHeader from '../components/PageHeader';
import { getErrorMessage } from '../utils/errors';
import { formatShortDateTime } from '../utils/format';

export default function Runbooks() {
  const [runbooks, setRunbooks] = useState<Runbook[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showModal, setShowModal] = useState(false);
  const [editingRunbook, setEditingRunbook] = useState<Runbook | null>(null);
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [saving, setSaving] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState<number | null>(null);

  // Load runbooks
  const loadRunbooks = async () => {
    try {
      setLoading(true);
      const data = await runbooksApi.list();
      setRunbooks(data);
      setError(null);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load runbooks'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadRunbooks();
  }, []);

  const openCreate = () => {
    setEditingRunbook(null);
    setTitle('');
    setContent('');
    setShowModal(true);
  };

  const openEdit = (runbook: Runbook) => {
    setEditingRunbook(runbook);
    setTitle(runbook.title);
    setContent(runbook.content);
    setShowModal(true);
  };

  const handleSave = async () => {
    if (!title.trim() || !content.trim()) return;
    setSaving(true);
    try {
      if (editingRunbook) {
        await runbooksApi.update(editingRunbook.id, { title: title.trim(), content });
      } else {
        await runbooksApi.create({ title: title.trim(), content });
      }
      setShowModal(false);
      loadRunbooks();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save runbook'));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      await runbooksApi.delete(id);
      setDeleteConfirm(null);
      loadRunbooks();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to delete runbook'));
    }
  };

  return (
    <div>
      <PageHeader
        title="Runbooks"
        description="Manage runbooks (SOPs) that the AI agent references during incident investigations"
      />

      {error && (
        <div className="mb-4 p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg text-red-700 dark:text-red-400 text-sm">
          {error}
          <button onClick={() => setError(null)} className="ml-2 font-medium hover:underline">Dismiss</button>
        </div>
      )}

      {/* Actions bar */}
      <div className="mb-6 flex justify-end">
        <button
          onClick={openCreate}
          className="flex items-center gap-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium"
        >
          <Plus size={16} />
          New Runbook
        </button>
      </div>

      {/* List */}
      {loading ? (
        <div className="text-center py-12 text-gray-500 dark:text-gray-400">Loading...</div>
      ) : runbooks.length === 0 ? (
        <div className="text-center py-12">
          <BookOpen size={48} className="mx-auto text-gray-300 dark:text-gray-600 mb-4" />
          <h3 className="text-lg font-medium text-gray-900 dark:text-white mb-2">No runbooks yet</h3>
          <p className="text-gray-500 dark:text-gray-400 mb-4">Create runbooks to guide the AI agent during incident investigations.</p>
          <button
            onClick={openCreate}
            className="inline-flex items-center gap-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium"
          >
            <Plus size={16} />
            Create your first runbook
          </button>
        </div>
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 divide-y divide-gray-200 dark:divide-gray-700">
          {runbooks.map((runbook) => (
            <div key={runbook.id} className="flex items-center justify-between p-4 hover:bg-gray-50 dark:hover:bg-gray-800/50">
              <div className="min-w-0 flex-1">
                <h3 className="text-sm font-medium text-gray-900 dark:text-white truncate">{runbook.title}</h3>
                <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">
                  Updated {formatShortDateTime(runbook.updated_at)}
                </p>
              </div>
              <div className="flex items-center gap-2 ml-4">
                <button
                  onClick={() => openEdit(runbook)}
                  className="p-1.5 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400 rounded-md hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                  title="Edit"
                >
                  <Pencil size={16} />
                </button>
                {deleteConfirm === runbook.id ? (
                  <div className="flex items-center gap-1">
                    <button
                      onClick={() => handleDelete(runbook.id)}
                      className="px-2 py-1 text-xs bg-red-600 text-white rounded hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onClick={() => setDeleteConfirm(null)}
                      className="px-2 py-1 text-xs bg-gray-200 dark:bg-gray-600 text-gray-700 dark:text-gray-200 rounded hover:bg-gray-300 dark:hover:bg-gray-500 transition-colors"
                    >
                      Cancel
                    </button>
                  </div>
                ) : (
                  <button
                    onClick={() => setDeleteConfirm(runbook.id)}
                    className="p-1.5 text-gray-400 hover:text-red-600 dark:hover:text-red-400 rounded-md hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                    title="Delete"
                  >
                    <Trash2 size={16} />
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Create/Edit Modal */}
      {showModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-xl w-full max-w-3xl mx-4 max-h-[90vh] flex flex-col">
            <div className="flex items-center justify-between p-4 border-b border-gray-200 dark:border-gray-700">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
                {editingRunbook ? 'Edit Runbook' : 'New Runbook'}
              </h2>
              <button
                onClick={() => setShowModal(false)}
                className="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 rounded-md"
              >
                <X size={20} />
              </button>
            </div>
            <div className="p-4 flex-1 overflow-auto space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Title</label>
                <input
                  type="text"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder="e.g., MySQL High CPU Troubleshooting"
                  className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white placeholder-gray-400 focus:ring-2 focus:ring-primary-500 focus:border-transparent text-sm"
                />
              </div>
              <div className="flex-1">
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Content (Markdown)</label>
                <textarea
                  value={content}
                  onChange={(e) => setContent(e.target.value)}
                  placeholder="Write your runbook procedures in markdown..."
                  rows={20}
                  className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white placeholder-gray-400 focus:ring-2 focus:ring-primary-500 focus:border-transparent text-sm font-mono"
                />
              </div>
            </div>
            <div className="flex justify-end gap-3 p-4 border-t border-gray-200 dark:border-gray-700">
              <button
                onClick={() => setShowModal(false)}
                className="px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 bg-gray-100 dark:bg-gray-700 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-600 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleSave}
                disabled={saving || !title.trim() || !content.trim()}
                className="px-4 py-2 text-sm font-medium text-white bg-primary-600 rounded-lg hover:bg-primary-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {saving ? 'Saving...' : editingRunbook ? 'Update' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
