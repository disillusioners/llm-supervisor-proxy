import { useState } from 'preact/hooks';
import type { ProxyConfig, Model } from '../types';
import { parseDuration, formatDuration } from '../hooks';
import { escapeHtml } from '../utils/helpers';

interface ConfigModalProps {
  isOpen: boolean;
  onClose: () => void;
  config: ProxyConfig | null;
  onUpdateConfig: (config: Partial<ProxyConfig>) => Promise<void>;
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
  const [status, setStatus] = useState<{ type: 'success' | 'error'; message: string } | null>(null);

  // Proxy Settings state
  const [upstreamUrl, setUpstreamUrl] = useState('');
  const [idleTimeout, setIdleTimeout] = useState('');
  const [maxRetries, setMaxRetries] = useState(0);
  const [maxGenTime, setMaxGenTime] = useState('');

  // Sync state when modal opens
  const handleOpen = () => {
    if (config) {
      setUpstreamUrl(config.UpstreamURL);
      setIdleTimeout(formatDuration(config.IdleTimeout));
      setMaxRetries(config.MaxRetries);
      setMaxGenTime(formatDuration(config.MaxGenerationTime));
    }
    setStatus(null);
  };

  if (!isOpen) return null;

  // Handle backdrop click
  const handleBackdropClick = (e: MouseEvent) => {
    if (e.target === e.currentTarget) {
      onClose();
    }
  };

  // Proxy Settings handlers
  const handleApplyProxy = async () => {
    try {
      setStatus(null);
      await onUpdateConfig({
        UpstreamURL: upstreamUrl,
        IdleTimeout: parseDuration(idleTimeout),
        MaxRetries: maxRetries,
        MaxGenerationTime: parseDuration(maxGenTime),
      });
      setStatus({ type: 'success', message: 'Configuration updated successfully' });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update config' });
    }
  };

  // Models handlers
  const handleAddModel = async () => {
    const name = prompt('Enter model name:');
    if (!name) return;
    
    const id = prompt('Enter model ID:');
    if (!id) return;
    
    const fallbackChain = prompt('Enter fallback chain (comma-separated model IDs, empty for none):') || '';
    const fallback = fallbackChain.split(',').map(s => s.trim()).filter(Boolean);
    
    try {
      setStatus(null);
      await onAddModel({
        id,
        name,
        enabled: true,
        fallback_chain: fallback,
      });
      setStatus({ type: 'success', message: 'Model added successfully' });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to add model' });
    }
  };

  const handleEditModel = async (model: Model) => {
    const name = prompt('Enter model name:', model.name);
    if (name === null) return;
    
    const fallbackChain = prompt('Enter fallback chain (comma-separated model IDs):', model.fallback_chain.join(', ')) || '';
    const fallback = fallbackChain.split(',').map(s => s.trim()).filter(Boolean);
    
    try {
      setStatus(null);
      await onUpdateModel(model.id, {
        name,
        fallback_chain: fallback,
      });
      setStatus({ type: 'success', message: 'Model updated successfully' });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update model' });
    }
  };

  const handleDeleteModel = async (model: Model) => {
    if (!confirm(`Delete model "${model.name}"?`)) return;
    
    try {
      setStatus(null);
      await onDeleteModel(model.id);
      setStatus({ type: 'success', message: 'Model deleted successfully' });
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
      onMouseEnter={handleOpen}
    >
      <div class="bg-gray-800 rounded-lg shadow-xl max-w-2xl w-full mx-4 max-h-[80vh] flex flex-col">
        {/* Header */}
        <div class="flex items-center justify-between px-6 py-4 border-b border-gray-700">
          <h2 class="text-xl font-semibold text-white">Configuration</h2>
          <button
            onClick={onClose}
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
            class={`px-6 py-3 font-medium transition-colors ${
              activeTab === 'proxy'
                ? 'text-blue-400 border-b-2 border-blue-400'
                : 'text-gray-400 hover:text-white'
            }`}
            onClick={() => setActiveTab('proxy')}
          >
            Proxy Settings
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors ${
              activeTab === 'models'
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
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                    placeholder="https://api.openai.com/v1"
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
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                    placeholder="30s"
                  />
                </div>
              </div>

              {/* Max Retries */}
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">Max Retries</label>
                <input
                  type="number"
                  value={maxRetries}
                  onInput={(e) => setMaxRetries(parseInt((e.target as HTMLInputElement).value) || 0)}
                  class="w-full px-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                  placeholder="3"
                />
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
                    class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                    placeholder="120s"
                  />
                </div>
              </div>

              {/* Apply Button */}
              <button
                onClick={handleApplyProxy}
                class="w-full bg-blue-600 hover:bg-blue-700 text-white font-medium py-2 px-4 rounded-md transition-colors"
              >
                Apply Changes
              </button>
            </div>
          )}

          {activeTab === 'models' && (
            <div class="space-y-4">
              {/* Add Model Button */}
              <button
                onClick={handleAddModel}
                class="bg-blue-600 hover:bg-blue-700 text-white font-medium py-2 px-4 rounded-md transition-colors"
              >
                Add Model
              </button>

              {/* Models List */}
              <div class="space-y-2">
                {models.length === 0 ? (
                  <p class="text-gray-400 text-center py-4">No models configured</p>
                ) : (
                  models.map((model) => (
                    <div
                      key={model.id}
                      class="flex items-center justify-between bg-gray-700 rounded-md p-3"
                    >
                      <div class="flex items-center gap-3 flex-1 min-w-0">
                        <button
                          onClick={() => handleToggleModel(model)}
                          class={`w-3 h-3 rounded-full flex-shrink-0 ${
                            model.enabled ? 'bg-green-500' : 'bg-gray-500'
                          }`}
                          title={model.enabled ? 'Enabled' : 'Disabled'}
                        />
                        <div class="flex-1 min-w-0">
                          <p class="text-white font-medium truncate">{escapeHtml(model.name)}</p>
                          <p class="text-gray-400 text-sm truncate">{escapeHtml(model.id)}</p>
                          {model.fallback_chain.length > 0 && (
                            <p class="text-gray-500 text-xs truncate">
                              Fallback: {escapeHtml(model.fallback_chain.join(', '))}
                            </p>
                          )}
                        </div>
                      </div>
                      <div class="flex items-center gap-2 flex-shrink-0 ml-2">
                        <button
                          onClick={() => handleEditModel(model)}
                          class="text-gray-400 hover:text-blue-400 transition-colors p-1"
                          title="Edit"
                        >
                          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                          </svg>
                        </button>
                        <button
                          onClick={() => handleDeleteModel(model)}
                          class="text-gray-400 hover:text-red-400 transition-colors p-1"
                          title="Delete"
                        >
                          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                          </svg>
                        </button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
          )}

          {/* Status Message */}
          {status && (
            <div
              class={`mt-4 p-3 rounded-md ${
                status.type === 'success'
                  ? 'bg-green-900/50 text-green-300 border border-green-700'
                  : 'bg-red-900/50 text-red-300 border border-red-700'
              }`}
            >
              {status.message}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
