import { Plus, Edit2, Trash2, Wrench } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import ToolFormSection from '../components/tools/ToolFormSection';
import { useToolManagement } from '../hooks/useToolManagement';
import { useSSHKeyManagement } from '../hooks/useSSHKeyManagement';

export default function Tools() {
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

  const handleEdit = async (tool: any) => {
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
      <PageHeader
        title="Tools"
        description="Manage tool instances and their configurations"
        action={
          !isCreating && !editingTool && (
            <button onClick={handleCreate} className="btn btn-primary">
              <Plus className="w-4 h-4" />
              New Tool
            </button>
          )
        }
      />

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
          <div className="card">
            {tools.length === 0 ? (
              <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
                <Wrench className="w-12 h-12 mx-auto text-gray-400 mb-3" />
                <p className="text-gray-500 dark:text-gray-400">No tool instances yet</p>
                <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Create one to get started</p>
              </div>
            ) : (
              <div className="space-y-4">
                {tools.map((tool) => (
                  <div
                    key={tool.id}
                    className={`border rounded-lg transition-all ${
                      tool.enabled
                        ? 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600'
                        : 'border-gray-100 dark:border-gray-800 opacity-60'
                    }`}
                  >
                    <div className="p-6">
                      <div className="flex items-start justify-between">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-3 mb-2">
                            <h3 className="font-semibold text-gray-900 dark:text-white">
                              {tool.name}
                            </h3>
                            <span className="badge badge-primary">
                              {tool.tool_type?.name}
                            </span>
                            <span className={`badge ${tool.enabled ? 'badge-success' : 'badge-default'}`}>
                              {tool.enabled ? 'Enabled' : 'Disabled'}
                            </span>
                          </div>
                          <p className="text-gray-600 dark:text-gray-400 text-sm">
                            {tool.tool_type?.description}
                          </p>
                        </div>

                        <div className="flex gap-2 ml-4 flex-shrink-0">
                          <button
                            onClick={() => handleEdit(tool)}
                            className="btn btn-ghost p-2 text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                            title="Edit"
                          >
                            <Edit2 className="w-4 h-4" />
                          </button>
                          <button
                            onClick={() => handleDelete(tool.id)}
                            className="btn btn-ghost p-2 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
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
