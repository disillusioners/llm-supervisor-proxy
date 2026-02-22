import { useState, useEffect } from 'preact/hooks';
import type { AppConfig, ConfigUpdateResponse, Model } from '../types';
import { escapeHtml } from '../utils/helpers';

interface ConfigModalProps {
  isOpen: boolean;
  onClose: () => void;
  config: AppConfig | null;
  onUpdateConfig: (config: Partial<AppConfig>) => Promise<ConfigUpdateResponse>;
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
}

type TabType = 'proxy' | 'models';

export function ConfigModal({
  isOpen,
  onClose,
  config,
  onUpdateConfig,
  models,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
}: ConfigModalProps) {
  const [activeTab, setActiveTab] = useState<TabType>('proxy');
  const [status, setStatus] = useState<{ type: 'success' | 'error'; message: string; restartRequired?: boolean } | null>(null);

  // Proxy Settings state
  const [upstreamUrl, setUpstreamUrl] = useState('');
  const [port, setPort] = useState<number>(8089);
  const [idleTimeout, setIdleTimeout] = useState('');
  const [maxUpstreamErrorRetries, setMaxUpstreamErrorRetries] = useState(0);
  const [maxIdleRetries, setMaxIdleRetries] = useState(0);
  const [maxGenerationRetries, setMaxGenerationRetries] = useState(0);
  const [maxGenTime, setMaxGenTime] = useState('');

  // Store original port to detect changes
  const [originalPort, setOriginalPort] = useState<number>(8089);

  // Model Form State
  const [showModelForm, setShowModelForm] = useState(false);
  const [modelFormMode, setModelFormMode] = useState<'add' | 'edit'>('add');
  const [modelFormData, setModelFormData] = useState<{ id: string; name: string; fallback_chain: string }>({ id: '', name: '', fallback_chain: '' });

  // Model delete confirmation state
  const [modelToDelete, setModelToDelete] = useState<Model | null>(null);

  // Sync state when modal opens or config changes
  useEffect(() => {
    if (isOpen && config) {
      setUpstreamUrl(config.upstream_url || '');
      setPort(config.port || 8089);
      setOriginalPort(config.port || 8089);
      setIdleTimeout(config.idle_timeout || '');
      setMaxUpstreamErrorRetries(config.max_upstream_error_retries || 0);
      setMaxIdleRetries(config.max_idle_retries || 0);
      setMaxGenerationRetries(config.max_generation_retries || 0);
      setMaxGenTime(config.max_generation_time || '');
    }
  }, [isOpen, config]);

  // Close modal and reset specific state
  const handleClose = () => {
    setStatus(null);
    setShowModelForm(false);
    setModelToDelete(null);
    onClose();
  };

  if (!isOpen) return null;

  // Handle backdrop click
  const handleBackdropClick = (e: MouseEvent) => {
    if (e.target === e.currentTarget) {
      handleClose();
    }
  };

  // Proxy Settings handlers
  const handleApplyProxy = async () => {
    try {
      setStatus(null);
      const response = await onUpdateConfig({
        upstream_url: upstreamUrl,
        port,
        idle_timeout: idleTimeout,
        max_upstream_error_retries: maxUpstreamErrorRetries,
        max_idle_retries: maxIdleRetries,
        max_generation_retries: maxGenerationRetries,
        max_generation_time: maxGenTime,
      });

      // Show success message, and also show restart warning if required
      if (response.restart_required) {
        setStatus({
          type: 'success',
          message: 'Configuration updated successfully. Server restart required for changes to take effect.',
          restartRequired: true
        });
      } else {
        setStatus({ type: 'success', message: 'Configuration updated successfully' });
      }
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update config' });
    }
  };

  // Model Handlers
  const handleOpenAddModel = () => {
    setModelFormData({ id: '', name: '', fallback_chain: '' });
    setModelFormMode('add');
    setShowModelForm(true);
    setStatus(null);
  };

  const handleOpenEditModel = (model: Model) => {
    setModelFormData({
      id: model.id,
      name: model.name,
      fallback_chain: model.fallback_chain.join(', ')
    });
    setModelFormMode('edit');
    setShowModelForm(true);
    setStatus(null);
  };

  const handleSaveModel = async () => {
    try {
      setStatus(null);
      const fallback = modelFormData.fallback_chain.split(',').map(s => s.trim()).filter(Boolean);

      if (modelFormMode === 'add') {
        if (!modelFormData.id || !modelFormData.name) {
          throw new Error('ID and Name are required');
        }
        await onAddModel({
          id: modelFormData.id,
          name: modelFormData.name,
          enabled: true,
          fallback_chain: fallback,
        });
        setStatus({ type: 'success', message: 'Model added successfully' });
      } else {
        if (!modelFormData.name) {
          throw new Error('Name is required');
        }
        await onUpdateModel(modelFormData.id, {
          name: modelFormData.name,
          fallback_chain: fallback,
        });
        setStatus({ type: 'success', message: 'Model updated successfully' });
      }
      setShowModelForm(false);
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

  const handleToggleModel = async (model: Model) => {
    try {
      setStatus(null);
      await onUpdateModel(model.id, { enabled: !model.enabled });
      setStatus({ type: 'success', message: 'Model toggled successfully' });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to toggle model' });
    }
  };

  return (
    <div
      class="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50"
      onClick={handleBackdropClick}
    >
      <div class="bg-gray-800 rounded-lg shadow-xl max-w-2xl w-full mx-4 max-h-[80vh] flex flex-col">
        {/* Header */}
        <div class="flex items-center justify-between px-6 py-4 border-b border-gray-700">
          <h2 class="text-xl font-semibold text-white">Configuration</h2>
          <button
            onClick={handleClose}
            class="text-gray-400 hover:text-white transition-colors"
          >
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Tabs */}
        <div class="flex border-b border-gray-700">
          <button
            class={`px-6 py-3 font-medium transition-colors ${activeTab === 'proxy'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('proxy')}
          >
            Proxy Settings
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors ${activeTab === 'models'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('models')}
          >
            Models
          </button>
        </div>

        {/* Content */}
        <div class="flex-1 overflow-y-auto p-6">
          {activeTab === 'proxy' && (
            <div class="space-y-4">
              {/* Upstream URL */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Upstream URL</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
                    </svg>
                  </span>
                  <input
                    type="text"
                    value={upstreamUrl}
                    onInput={(e) => setUpstreamUrl((e.target as HTMLInputElement).value)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="https://api.openai.com/v1"
                  />
                </div>
              </div>

              {/* Port */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1 flex items-center justify-between">
                  <span>Port</span>
                  {port !== originalPort && (
                    <span class="text-yellow-500 text-xs bg-yellow-500/10 px-2 py-0.5 rounded">⚠️ Requires restart</span>
                  )}
                </label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M12 5v14" />
                    </svg>
                  </span>
                  <input
                    type="number"
                    value={port}
                    onInput={(e) => setPort(parseInt((e.target as HTMLInputElement).value) || 8089)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="8089"
                  />
                </div>
              </div>

              {/* Idle Timeout */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Idle Timeout</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </span>
                  <input
                    type="text"
                    value={idleTimeout}
                    onInput={(e) => setIdleTimeout((e.target as HTMLInputElement).value)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="30s"
                  />
                </div>
              </div>

              {/* Max Retries */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Max Error Retries</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                    </svg>
                  </span>
                  <input
                    type="number"
                    value={maxUpstreamErrorRetries}
                    onInput={(e) => setMaxUpstreamErrorRetries(parseInt((e.target as HTMLInputElement).value) || 0)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="3"
                  />
                </div>
              </div>

              {/* Max Idle Retries */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Max Idle Retries</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </span>
                  <input
                    type="number"
                    value={maxIdleRetries}
                    onInput={(e) => setMaxIdleRetries(parseInt((e.target as HTMLInputElement).value) || 0)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="2"
                  />
                </div>
              </div>

              {/* Max Generation Retries */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Max Generation Retries</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </span>
                  <input
                    type="number"
                    value={maxGenerationRetries}
                    onInput={(e) => setMaxGenerationRetries(parseInt((e.target as HTMLInputElement).value) || 0)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="2"
                  />
                </div>
              </div>

              {/* Max Generation Time */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Max Generation Time</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </span>
                  <input
                    type="text"
                    value={maxGenTime}
                    onInput={(e) => setMaxGenTime((e.target as HTMLInputElement).value)}
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                    placeholder="120s"
                  />
                </div>
              </div>

              {/* Apply Button */}
              <div class="pt-2">
                <button
                  onClick={handleApplyProxy}
                  class="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium py-2 px-4 rounded-md transition-colors shadow shadow-blue-900/20"
                >
                  Apply Changes
                </button>
              </div>
            </div>
          )}

          {activeTab === 'models' && (
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
                              onClick={() => handleToggleModel(model)}
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
                              </p>
                              <p class="text-gray-400 text-sm truncate font-mono bg-gray-800/50 px-1 py-0.5 rounded mt-1 inline-block">
                                {escapeHtml(model.id)}
                              </p>
                              {model.fallback_chain.length > 0 && (
                                <div class="mt-1 flex items-center gap-1.5 flex-wrap">
                                  <span class="text-xs text-gray-500 font-medium">FALLBACKS:</span>
                                  {model.fallback_chain.map(fb => (
                                    <span class="text-xs bg-gray-600 text-gray-200 px-1.5 py-0.5 rounded">
                                      {escapeHtml(fb)}
                                    </span>
                                  ))}
                                </div>
                              )}
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
                <div class="bg-gray-700/50 rounded-lg p-5 border border-gray-600">
                  <h3 class="text-lg font-medium text-white mb-4">
                    {modelFormMode === 'add' ? 'Add New Model' : 'Edit Model'}
                  </h3>
                  <div class="space-y-4">
                    {modelFormMode === 'add' && (
                      <div>
                        <label class="block text-sm font-medium text-gray-300 mb-1">Model ID <span class="text-red-400">*</span></label>
                        <input
                          type="text"
                          value={modelFormData.id}
                          onInput={(e) => setModelFormData({ ...modelFormData, id: (e.target as HTMLInputElement).value })}
                          class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                          placeholder="e.g., gpt-4"
                        />
                      </div>
                    )}
                    <div>
                      <label class="block text-sm font-medium text-gray-300 mb-1">Display Name <span class="text-red-400">*</span></label>
                      <input
                        type="text"
                        value={modelFormData.name}
                        onInput={(e) => setModelFormData({ ...modelFormData, name: (e.target as HTMLInputElement).value })}
                        class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                        placeholder="e.g., GPT-4"
                      />
                    </div>
                    <div>
                      <label class="block text-sm font-medium text-gray-300 mb-1">Fallback Chain (comma-separated IDs)</label>
                      <input
                        type="text"
                        value={modelFormData.fallback_chain}
                        onInput={(e) => setModelFormData({ ...modelFormData, fallback_chain: (e.target as HTMLInputElement).value })}
                        class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                        placeholder="e.g., gpt-3.5-turbo, claude-2"
                      />
                      <p class="text-xs text-gray-400 mt-1">Leave empty for no fallbacks.</p>
                    </div>

                    <div class="flex justify-end gap-3 pt-2">
                      <button
                        onClick={() => setShowModelForm(false)}
                        class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
                      >
                        Cancel
                      </button>
                      <button
                        onClick={handleSaveModel}
                        class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium"
                        disabled={modelFormMode === 'add' ? !modelFormData.id || !modelFormData.name : !modelFormData.name}
                      >
                        {modelFormMode === 'add' ? 'Add Model' : 'Save Changes'}
                      </button>
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Status Message */}
          {status && (
            <div
              class={`mt-6 p-4 rounded-md shadow-sm border ${status.type === 'success'
                ? 'bg-green-900/30 text-green-300 border-green-800/50'
                : 'bg-red-900/30 text-red-300 border-red-800/50'
                }`}
            >
              <div class="flex items-start gap-2">
                {status.type === 'success' ? (
                  <svg class="w-5 h-5 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                ) : (
                  <svg class="w-5 h-5 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                )}
                <div>
                  <p class="font-medium">{status.message}</p>
                  {status.restartRequired && (
                    <p class="mt-1 text-sm text-yellow-300/90 font-medium flex items-center gap-1">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                      </svg>
                      Server restart required for changes to take effect
                    </p>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Delete Confirmation Dialog */}
      {modelToDelete && (
        <div class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]">
          <div class="bg-gray-800 rounded-lg shadow-2xl max-w-sm w-full mx-4 border border-gray-700 p-6 flex flex-col items-center text-center">
            <div class="w-12 h-12 bg-red-900/30 text-red-400 rounded-full flex items-center justify-center mb-4 border border-red-800/50">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
            </div>
            <h3 class="text-xl font-semibold text-white mb-2">Delete Model</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to delete <span class="font-semibold text-white">"{escapeHtml(modelToDelete.name)}"</span>? This action cannot be undone.
            </p>
            <div class="flex gap-3 w-full">
              <button
                onClick={() => setModelToDelete(null)}
                class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
              >
                Cancel
              </button>
              <button
                onClick={handleConfirmDeleteModel}
                class="flex-1 px-4 py-2.5 bg-red-600 hover:bg-red-500 text-white rounded-lg transition-colors font-medium border border-red-500/50 shadow shadow-red-900/20"
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
