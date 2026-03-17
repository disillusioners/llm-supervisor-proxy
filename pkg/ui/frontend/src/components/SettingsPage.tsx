import { useState, useEffect } from 'preact/hooks';
import { route } from 'preact-router';
import type { AppConfig, ConfigUpdateResponse, Model, ApiToken, LoopDetectionConfig, ToolRepairConfig, Credential } from '../types';
import { getCredentials } from '../hooks/useApi';
import { ProxySettings } from './config/ProxySettings';
import { ModelsTab } from './config/ModelsTab';
import { CredentialsTab } from './config/CredentialsTab';
import { LoopDetectionSettings } from './config/LoopDetectionSettings';
import { ToolRepairSettings } from './config/ToolRepairSettings';
import { TokenList } from './tokens/TokenList';
import { TokenForm } from './tokens/TokenForm';
import { ToastContainer, type ToastData } from './Toast';

interface SettingsPageProps {
  config: AppConfig | null;
  onUpdateConfig: (config: Partial<AppConfig>) => Promise<ConfigUpdateResponse>;
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  tokens: ApiToken[];
  onCreateToken: (name: string, expiresAt: string | null) => Promise<ApiToken>;
  onDeleteToken: (id: string) => Promise<void>;
  onRefetchTokens: () => void;
}

type TabType = 'proxy' | 'models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens';

// Helper to generate unique toast IDs
const generateToastId = () => `toast-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;

export function SettingsPage({
  config,
  onUpdateConfig,
  models,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
  tokens,
  onCreateToken,
  onDeleteToken,
  onRefetchTokens,
}: SettingsPageProps) {
  const [activeTab, setActiveTab] = useState<TabType>('proxy');
  const [toasts, setToasts] = useState<ToastData[]>([]);

  // Helper to add a toast
  const addToast = (type: ToastData['type'], message: string, restartRequired?: boolean) => {
    const newToast: ToastData = {
      id: generateToastId(),
      type,
      message,
      restartRequired,
    };
    setToasts(prev => [...prev, newToast]);
  };

  // Helper to dismiss a toast
  const dismissToast = (id: string) => {
    setToasts(prev => prev.filter(t => t.id !== id));
  };

  // Create a setStatus-like function for child components
  const setStatusWrapper = (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => {
    if (status) {
      addToast(status.type, status.message, status.restartRequired);
    }
  };

  // Proxy Settings state
  const [upstreamUrl, setUpstreamUrl] = useState('');
  const [upstreamCredentialId, setUpstreamCredentialId] = useState('');
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [port, setPort] = useState<number>(8089);
  const [idleTimeout, setIdleTimeout] = useState('');
  const [streamDeadline, setStreamDeadline] = useState('');
  const [maxGenTime, setMaxGenTime] = useState('');
  // Race retry state
  const [raceRetryEnabled, setRaceRetryEnabled] = useState(false);
  const [raceParallelOnIdle, setRaceParallelOnIdle] = useState(true);
  const [raceMaxParallel, setRaceMaxParallel] = useState(3);
  const [raceMaxBufferBytes, setRaceMaxBufferBytes] = useState(5 * 1024 * 1024);
  // Ultimate model state
  const [ultimateModelId, setUltimateModelId] = useState('');
  const [ultimateModelMaxHash, setUltimateModelMaxHash] = useState(100);
  const [ultimateModelMaxRetries, setUltimateModelMaxRetries] = useState(2);

  // Store original port to detect changes
  const [originalPort, setOriginalPort] = useState<number>(8089);

  // Model delete confirmation state
  const [modelToDelete, setModelToDelete] = useState<Model | null>(null);

  // Token state
  const [showTokenForm, setShowTokenForm] = useState(false);
  const [newToken, setNewToken] = useState<ApiToken | null>(null);
  const [showTokenValue, setShowTokenValue] = useState(false);

  // Sync state when config changes
  useEffect(() => {
    if (config) {
      setUpstreamUrl(config.upstream_url || '');
      setUpstreamCredentialId(config.upstream_credential_id || '');
      setPort(config.port || 8089);
      setOriginalPort(config.port || 8089);
      setIdleTimeout(config.idle_timeout || '');
      setStreamDeadline(config.stream_deadline || '');
      setMaxGenTime(config.max_generation_time || '');
      // Race retry sync
      setRaceRetryEnabled(config.race_retry_enabled ?? false);
      setRaceParallelOnIdle(config.race_parallel_on_idle ?? true);
      setRaceMaxParallel(config.race_max_parallel ?? 3);
      setRaceMaxBufferBytes(config.race_max_buffer_bytes ?? 5242880);
      // Ultimate model sync
      setUltimateModelId(config.ultimate_model?.model_id || '');
      setUltimateModelMaxHash(config.ultimate_model?.max_hash || 100);
      setUltimateModelMaxRetries(config.ultimate_model?.max_retries ?? 2);
    }
  }, [config]);

  // Fetch credentials on mount
  useEffect(() => {
    const fetchCredentials = async () => {
      try {
        const data = await getCredentials();
        setCredentials(data || []);
      } catch (e) {
        console.error('Failed to fetch credentials:', e);
      }
    };
    fetchCredentials();
  }, []);

  // Proxy Settings handlers
  const handleApplyProxy = async () => {
    try {
      const response = await onUpdateConfig({
        upstream_url: upstreamUrl,
        upstream_credential_id: upstreamCredentialId,
        port,
        idle_timeout: idleTimeout,
        stream_deadline: streamDeadline,
        max_generation_time: maxGenTime,
        // Race retry configuration
        race_retry_enabled: raceRetryEnabled,
        race_parallel_on_idle: raceParallelOnIdle,
        race_max_parallel: raceMaxParallel,
        race_max_buffer_bytes: raceMaxBufferBytes,
        // Ultimate model
        ultimate_model: {
          model_id: ultimateModelId,
          max_hash: ultimateModelMaxHash,
          max_retries: ultimateModelMaxRetries,
        },
      });

      // Show success message, and also show restart warning if required
      if (response.restart_required) {
        addToast('success', 'Configuration updated successfully. Server restart required for changes to take effect.', true);
      } else {
        addToast('success', 'Configuration updated successfully');
      }
    } catch (e) {
      addToast('error', e instanceof Error ? e.message : 'Failed to update config');
    }
  };

  // Model handlers
  const handleToggleModel = async (model: Model) => {
    try {
      await onUpdateModel(model.id, { enabled: !model.enabled });
      addToast('success', 'Model toggled successfully');
    } catch (e) {
      addToast('error', e instanceof Error ? e.message : 'Failed to toggle model');
    }
  };

  // Loop Detection handler
  const handleApplyLoopDetection = async (loopConfig: LoopDetectionConfig) => {
    try {
      const response = await onUpdateConfig({
        loop_detection: loopConfig,
      });

      if (response.restart_required) {
        addToast('success', 'Loop detection configuration updated. Server restart required.', true);
      } else {
        addToast('success', 'Loop detection configuration updated');
      }
    } catch (e) {
      addToast('error', e instanceof Error ? e.message : 'Failed to update loop detection config');
    }
  };

  // Tool Repair handler
  const handleApplyToolRepair = async (toolRepairConfig: ToolRepairConfig) => {
    try {
      const response = await onUpdateConfig({
        tool_repair: toolRepairConfig,
      });

      if (response.restart_required) {
        addToast('success', 'Tool repair configuration updated. Server restart required.', true);
      } else {
        addToast('success', 'Tool repair configuration updated');
      }
    } catch (e) {
      addToast('error', e instanceof Error ? e.message : 'Failed to update tool repair config');
    }
  };

  // Token handlers
  const handleCreateToken = async (name: string, expiresAt: string | null) => {
    try {
      const token = await onCreateToken(name, expiresAt);
      setNewToken(token);
      setShowTokenValue(true);
      onRefetchTokens();
    } catch (e) {
      throw e;
    }
  };

  const handleRevokeToken = async (id: string) => {
    await onDeleteToken(id);
  };

  const handleCopyToken = () => {
    if (newToken?.token) {
      navigator.clipboard.writeText(newToken.token);
      addToast('success', 'Token copied to clipboard');
    }
  };

  const handleCloseTokenModal = () => {
    setShowTokenValue(false);
    setNewToken(null);
    setShowTokenForm(false);
  };

  const handleGoBack = () => {
    route('/ui/');
  };

  return (
    <div class="min-h-screen bg-gray-900 flex flex-col">
      {/* Header with back button */}
      <header class="bg-gray-800 border-b border-gray-700 px-6 py-4 flex items-center gap-4">
        <button
          onClick={handleGoBack}
          class="text-gray-400 hover:text-white transition-colors flex items-center gap-2"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 19l-7-7m0 0l7-7m-7 7h18" />
          </svg>
          <span>Back</span>
        </button>
        <h1 class="text-xl font-semibold text-white">Settings</h1>
      </header>

      {/* Tabs */}
      <div class="bg-gray-800 border-b border-gray-700 px-6">
        <div class="flex overflow-x-auto">
          <button
            class={`px-6 py-3 font-medium transition-colors whitespace-nowrap ${activeTab === 'proxy'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('proxy')}
          >
            Proxy Settings
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors whitespace-nowrap ${activeTab === 'models'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('models')}
          >
            Models
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors flex items-center gap-1.5 whitespace-nowrap ${activeTab === 'credentials'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('credentials')}
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
            </svg>
            Credentials
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors whitespace-nowrap ${activeTab === 'loop_detection'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('loop_detection')}
          >
            Loop Detection
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors whitespace-nowrap ${activeTab === 'tool_repair'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('tool_repair')}
          >
            Tool Repair
          </button>
          <button
            class={`px-6 py-3 font-medium transition-colors whitespace-nowrap ${activeTab === 'tokens'
              ? 'text-blue-400 border-b-2 border-blue-400'
              : 'text-gray-400 hover:text-white'
              }`}
            onClick={() => setActiveTab('tokens')}
          >
            Tokens
          </button>
        </div>
      </div>

      {/* Content */}
      <div class="flex-1 overflow-y-auto p-6">
        <div class="max-w-4xl mx-auto">
          {activeTab === 'proxy' && (
            <ProxySettings
              upstreamUrl={upstreamUrl}
              upstreamCredentialId={upstreamCredentialId}
              credentials={credentials}
              models={models}
              port={port}
              idleTimeout={idleTimeout}
              streamDeadline={streamDeadline}
              maxGenTime={maxGenTime}
              originalPort={originalPort}
              raceRetryEnabled={raceRetryEnabled}
              raceParallelOnIdle={raceParallelOnIdle}
              raceMaxParallel={raceMaxParallel}
              raceMaxBufferBytes={raceMaxBufferBytes}
              ultimateModelId={ultimateModelId}
              ultimateModelMaxHash={ultimateModelMaxHash}
              ultimateModelMaxRetries={ultimateModelMaxRetries}
              onUpstreamUrlChange={setUpstreamUrl}
              onUpstreamCredentialIdChange={setUpstreamCredentialId}
              onPortChange={setPort}
              onIdleTimeoutChange={setIdleTimeout}
              onStreamDeadlineChange={setStreamDeadline}
              onMaxGenTimeChange={setMaxGenTime}
              onRaceRetryEnabledChange={setRaceRetryEnabled}
              onRaceParallelOnIdleChange={setRaceParallelOnIdle}
              onRaceMaxParallelChange={setRaceMaxParallel}
              onRaceMaxBufferBytesChange={setRaceMaxBufferBytes}
              onUltimateModelIdChange={setUltimateModelId}
              onUltimateModelMaxHashChange={setUltimateModelMaxHash}
              onUltimateModelMaxRetriesChange={setUltimateModelMaxRetries}
              onApply={handleApplyProxy}
              setStatus={setStatusWrapper}
            />
          )}

          {activeTab === 'models' && (
            <ModelsTab
              models={models}
              onAddModel={onAddModel}
              onUpdateModel={onUpdateModel}
              onDeleteModel={onDeleteModel}
              onToggleModel={handleToggleModel}
              setStatus={setStatusWrapper}
              onNavigateToCredentials={() => setActiveTab('credentials')}
            />
          )}

          {activeTab === 'credentials' && (
            <CredentialsTab
              setStatus={setStatusWrapper}
            />
          )}

          {activeTab === 'loop_detection' && (
            <LoopDetectionSettings
              config={config?.loop_detection ?? null}
              onApply={handleApplyLoopDetection}
              setStatus={setStatusWrapper}
            />
          )}

          {activeTab === 'tool_repair' && (
            <ToolRepairSettings
              config={config?.tool_repair ?? null}
              models={models}
              onApply={handleApplyToolRepair}
              setStatus={setStatusWrapper}
            />
          )}

          {activeTab === 'tokens' && (
            <>
              {!showTokenForm ? (
                <TokenList
                  tokens={tokens}
                  onRevoke={handleRevokeToken}
                  onStatus={setStatusWrapper}
                  onCreateToken={() => setShowTokenForm(true)}
                />
              ) : (
                <TokenForm
                  onSubmit={handleCreateToken}
                  onCancel={() => setShowTokenForm(false)}
                  onStatus={setStatusWrapper}
                />
              )}
            </>
          )}
        </div>

        {/* Toast notifications */}
        <ToastContainer toasts={toasts} onDismiss={dismissToast} />
      </div>

      {/* Token Value Modal - Show once after creation */}
      {showTokenValue && newToken && (
        <div class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50">
          <div class="bg-gray-800 rounded-lg shadow-2xl max-w-md w-full mx-4 border border-gray-700 p-6">
            <div class="flex flex-col items-center text-center">
              <div class="w-12 h-12 bg-green-900/30 text-green-400 rounded-full flex items-center justify-center mb-4 border border-green-800/50">
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
              <h3 class="text-xl font-semibold text-white mb-2">Token Created</h3>
              <p class="text-gray-300 mb-4">
                Your new API token has been created. Copy it now as you won't be able to see it again.
              </p>
              
              <div class="w-full bg-gray-900 rounded-md p-3 mb-4 border border-gray-700">
                <code class="text-green-400 font-mono text-sm break-all">
                  {newToken.token}
                </code>
              </div>

              <div class="flex gap-3 w-full">
                <button
                  onClick={handleCloseTokenModal}
                  class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
                >
                  Close
                </button>
                <button
                  onClick={handleCopyToken}
                  class="flex-1 px-4 py-2.5 bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors font-medium border border-blue-500/50 shadow shadow-blue-900/20 flex items-center justify-center gap-2"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                  Copy
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
