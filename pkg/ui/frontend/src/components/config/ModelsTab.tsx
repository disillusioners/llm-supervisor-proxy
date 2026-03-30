import { useState } from 'preact/hooks';
import type { Model } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { ModelForm } from './ModelForm';

interface ModelsTabProps {
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  onToggleModel: (model: Model) => Promise<void>;
  status: { type: 'success' | 'error'; message: string } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onNavigateToCredentials?: () => void;
}

export function ModelsTab({
  models,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
  onToggleModel,
  status,
  setStatus,
  onNavigateToCredentials,
}: ModelsTabProps) {
  const [showModelForm, setShowModelForm] = useState(false);
  const [modelFormMode, setModelFormMode] = useState<'add' | 'edit'>('add');
  const [modelToEdit, setModelToEdit] = useState<Model | undefined>(undefined);
  const [modelToDelete, setModelToDelete] = useState<Model | null>(null);

  const handleOpenAddModel = () => {
    setModelToEdit(undefined);
    setModelFormMode('add');
    setShowModelForm(true);
    setStatus(null);
  };

  const handleOpenEditModel = (model: Model) => {
    setModelToEdit(model);
    setModelFormMode('edit');
    setShowModelForm(true);
    setStatus(null);
  };

  const handleSaveModel = async (data: {
    id: string;
    name: string;
    fallback_chain: string[];
    truncate_params: string[];
    internal?: boolean;
    credential_id?: string;
    internal_provider?: 'openai' | 'zhipu' | 'azure';
    internal_api_key?: string;
    internal_base_url?: string;
    internal_model?: string;
    release_stream_chunk_deadline?: string;
  }) => {
    try {
      if (modelFormMode === 'add') {
        await onAddModel({
          id: data.id,
          name: data.name,
          enabled: true,
          fallback_chain: data.fallback_chain,
          truncate_params: data.truncate_params,
          internal: data.internal,
          internal_provider: data.internal_provider,
          internal_api_key: data.internal_api_key,
          internal_base_url: data.internal_base_url,
          internal_model: data.internal_model,
          release_stream_chunk_deadline: data.release_stream_chunk_deadline,
        });
        setStatus({ type: 'success', message: 'Model added successfully' });
      } else {
        await onUpdateModel(data.id, {
          name: data.name,
          fallback_chain: data.fallback_chain,
          truncate_params: data.truncate_params,
          internal: data.internal,
          credential_id: data.internal ? data.credential_id : undefined,
          internal_provider: data.internal_provider,
          internal_api_key: data.internal_api_key,
          internal_base_url: data.internal_base_url,
          internal_model: data.internal_model,
          release_stream_chunk_deadline: data.release_stream_chunk_deadline,
          peak_hour_enabled: data.peak_hour_enabled,
          peak_hour_start: data.peak_hour_start,
          peak_hour_end: data.peak_hour_end,
          peak_hour_timezone: data.peak_hour_timezone,
          peak_hour_model: data.peak_hour_model,
        });
        setStatus({ type: 'success', message: 'Model updated successfully' });
      }
      setShowModelForm(false);
      setModelToEdit(undefined);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save model' });
    }
  };

  const handleConfirmDeleteModel = async () => {
    if (!modelToDelete) return;
    try {
      setStatus(null);
      await onDeleteModel(modelToDelete.id);
      setStatus({ type: 'success', message: 'Model deleted successfully' });
      setModelToDelete(null);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to delete model' });
    }
  };

  return (
    <div class="space-y-4">
      {!showModelForm ? (
        <>
          <div class="flex justify-between items-center mb-2">
            <h3 class="text-white font-medium">Available Models</h3>
            <button
              onClick={handleOpenAddModel}
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Model
            </button>
          </div>

          {/* Models List */}
          <div class="space-y-2">
            {models.length === 0 ? (
              <div class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center">
                <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 002-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
                </svg>
                <p class="text-gray-400 text-sm">No models configured</p>
              </div>
            ) : (
              models.map((model) => (
                <div
                  key={model.id}
                  class="flex items-center justify-between bg-gray-700/80 rounded-md p-3 border border-gray-600/50 hover:bg-gray-700 transition-colors"
                >
                  <div class="flex items-center gap-3 flex-1 min-w-0">
                    <button
                      onClick={() => onToggleModel(model)}
                      class={`w-10 h-6 rounded-full flex-shrink-0 relative transition-colors ${model.enabled ? 'bg-green-500' : 'bg-gray-500'
                        }`}
                      title={model.enabled ? 'Enabled' : 'Disabled'}
                    >
                      <span class={`absolute top-1 w-4 h-4 bg-white rounded-full transition-all ${model.enabled ? 'right-1' : 'left-1'
                        }`}></span>
                    </button>
                    <div class="flex-1 min-w-0">
                      <p class="text-gray-100 font-medium truncate flex items-center gap-2">
                        {escapeHtml(model.name)}
                        {model.internal && (
                          <span class="inline-flex items-center gap-1 text-xs bg-purple-900/50 text-purple-300 border border-purple-700/50 px-1.5 py-0.5 rounded" title={`Internal upstream: ${model.internal_provider || 'unknown'}`}>
                            <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
                            </svg>
                            {model.internal_provider || 'internal'}
                          </span>
                        )}
                      </p>
                      <p class="text-gray-400 text-sm truncate font-mono bg-gray-800/50 px-1 py-0.5 rounded mt-1 inline-block">
                        {escapeHtml(model.id)}
                      </p>
                      {(model.fallback_chain ?? []).length > 0 && (
                        <div class="mt-1 flex items-center gap-1.5 flex-wrap">
                          <span class="text-xs text-gray-500 font-medium">FALLBACKS:</span>
                          {(model.fallback_chain ?? []).map(fb => (
                            <span class="text-xs bg-gray-600 text-gray-200 px-1.5 py-0.5 rounded">
                              {escapeHtml(fb)}
                            </span>
                          ))}
                        </div>
                      )}
                      {(model.truncate_params ?? []).length > 0 && (
                        <div class="mt-1 flex items-center gap-1.5 flex-wrap">
                          <span class="text-xs text-gray-500 font-medium">STRIP PARAMS:</span>
                          {(model.truncate_params ?? []).map(p => (
                            <span class="text-xs bg-purple-900/50 text-purple-300 border border-purple-800/40 px-1.5 py-0.5 rounded font-mono">
                              {escapeHtml(p)}
                            </span>
                          ))}
                        </div>
                      )}
                      {/* STREAM DEADLINE: Hidden - feature not used anymore, can be re-enabled later
                      {model.release_stream_chunk_deadline && (
                        <div class="mt-1 flex items-center gap-1.5 flex-wrap">
                          <span class="text-xs text-gray-500 font-medium">STREAM DEADLINE:</span>
                          <span class="text-xs bg-blue-900/50 text-blue-300 border border-blue-800/40 px-1.5 py-0.5 rounded font-mono">
                            {escapeHtml(model.release_stream_chunk_deadline)}
                          </span>
                        </div>
                      )}
                      */}
                    </div>
                  </div>
                  <div class="flex items-center gap-1 flex-shrink-0 ml-4">
                    <button
                      onClick={() => handleOpenEditModel(model)}
                      class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                      title="Edit model"
                    >
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                      </svg>
                    </button>
                    <button
                      onClick={() => setModelToDelete(model)}
                      class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                      title="Delete model"
                    >
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                      </svg>
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>
        </>
      ) : (
        <ModelForm
          mode={modelFormMode}
          initialData={modelToEdit}
          onSave={handleSaveModel}
          onCancel={() => {
            setShowModelForm(false);
            setModelToEdit(undefined);
          }}
          onStatus={setStatus}
          onNavigateToCredentials={onNavigateToCredentials}
        />
      )}
    </div>
  );
}
