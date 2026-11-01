import { Plus, Edit2, Trash2, Wrench } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import ToolFormSection from '../tools/ToolFormSection';
import { useToolManagement } from '../../hooks/useToolManagement';
import { useSSHKeyManagement } from '../../hooks/useSSHKeyManagement';
import EmptyState from '../shared/EmptyState';
import type { ToolInstance } from '../../types';

export default function ToolsSection() {
  const {
    tools,
    toolTypes,
    loading,
    error,
    setError,
    editingTool,
    isCreating,
    formData,
    setFormData,
    selectedType,
    selectedSchema,
    handleCreate,
    handleEdit: baseHandleEdit,
    handleSave,
    handleDelete,
    handleCancel: baseHandleCancel,
    updateSetting,
  } = useToolManagement();

  const ssh = useSSHKeyManagement(setError);

  const handleEdit = async (tool: ToolInstance) => {
    ssh.resetKeyForm();
    baseHandleEdit(tool);
    if (tool.tool_type?.name === 'ssh') {
      await ssh.loadSSHKeys(tool.id);
    }
  };

  const handleCancel = () => {
    ssh.resetKeyForm();
    baseHandleCancel();
  };

  return (
    <div>
      {!isCreating && !editingTool && (
        <div className="mb-4 flex justify-end">
          <button onClick={handleCreate} className="btn btn-primary">
            <Plus className="w-4 h-4" />
            New tool
          </button>
        </div>
      )}

      {error && <ErrorMessage message={error} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <>
          {/* Create/Edit Form */}
          {(isCreating || editingTool) && (
            <ToolFormSection
              isCreating={isCreating}
              formData={formData}
              setFormData={setFormData}
              updateSetting={updateSetting}
              toolTypes={toolTypes}
              selectedType={selectedType}
              selectedSchema={selectedSchema}
              editingToolId={editingTool?.id}
              sshKeys={ssh.sshKeys}
              sshKeysLoading={ssh.sshKeysLoading}
              showAddKey={ssh.showAddKey}
              setShowAddKey={ssh.setShowAddKey}
              newKeyName={ssh.newKeyName}
              setNewKeyName={ssh.setNewKeyName}
              newKeyValue={ssh.newKeyValue}
              setNewKeyValue={ssh.setNewKeyValue}
              newKeyIsDefault={ssh.newKeyIsDefault}
              setNewKeyIsDefault={ssh.setNewKeyIsDefault}
              onAddSSHKey={() => editingTool && ssh.handleAddSSHKey(editingTool.id)}
              onDeleteSSHKey={(keyId) => editingTool && ssh.handleDeleteSSHKey(editingTool.id, keyId)}
              onSetDefaultKey={(keyId) => editingTool && ssh.handleSetDefaultKey(editingTool.id, keyId)}
              getDefaultKey={ssh.getDefaultKey}
              onSave={handleSave}
              onCancel={handleCancel}
            />
          )}

          {/* Tools List */}
          <div className="card p-0 overflow-hidden">
            {tools.length === 0 ? (
              <div className="p-4"><EmptyState icon={Wrench} title="No tools yet" description="Add a tool so the agent can reach your systems." /></div>
            ) : (
              <div className="divide-y divide-line">
                {tools.map((tool) => (
                  <div
                    key={tool.id}
                    className={`hover:bg-surface-2 transition-colors ${tool.enabled ? '' : 'opacity-60'}`}
                  >
                    <div className="px-5 py-4">
                      <div className="flex items-start justify-between">
                        <div className="flex-1 min-w-0">
                          <div className="flex flex-wrap items-center gap-2 mb-1">
                            <h3 className="font-medium text-fg">
                              {tool.name}
                            </h3>
                            <span className="badge badge-primary">
                              {tool.tool_type?.name}
                            </span>
                            <span className={`badge ${tool.enabled ? 'badge-success' : 'badge-default'}`}>
                              {tool.enabled ? 'Enabled' : 'Disabled'}
                            </span>
                          </div>
                          <p className="text-fg-muted text-sm">
                            {tool.tool_type?.description}
                          </p>
                        </div>

                        <div className="flex gap-2 ml-4 flex-shrink-0">
                          <button
                            onClick={() => handleEdit(tool)}
                            className="btn btn-ghost p-2"
                            title="Edit"
                          >
                            <Edit2 className="w-4 h-4" />
                          </button>
                          <button
                            onClick={() => handleDelete(tool.id)}
                            className="btn btn-ghost p-2 text-state-fail hover:bg-state-fail/10"
                            title="Delete"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
