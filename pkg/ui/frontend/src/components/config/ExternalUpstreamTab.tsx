import { useState, useEffect } from 'preact/hooks';
import type { ExternalUpstream } from '../../types';
import { getExternalUpstream, updateExternalUpstream } from '../../hooks/useApi';

interface ExternalUpstreamTabProps {
  status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}

const PROVIDERS = ['openai', 'anthropic', 'gemini', 'zhipu', 'azure', 'zai', 'minimax'] as const;
type Provider = typeof PROVIDERS[number];

const PROVIDER_DEFAULTS: Record<Provider, string> = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com',
  gemini: 'https://generativelanguage.googleapis.com',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4',
  azure: '',
  zai: 'https://api.z.ai/api/coding/paas/v4',
  minimax: 'https://api.minimax.io/v1',
};

const providerColors: Record<Provider, string> = {
  openai: 'bg-green-900/50 text-green-300 border-green-800/40',
  anthropic: 'bg-orange-900/50 text-orange-300 border-orange-800/40',
  gemini: 'bg-blue-900/50 text-blue-300 border-blue-800/40',
  zhipu: 'bg-purple-900/50 text-purple-300 border-purple-800/40',
  azure: 'bg-cyan-900/50 text-cyan-300 border-cyan-800/40',
  zai: 'bg-red-900/50 text-red-300 border-red-800/40',
  minimax: 'bg-yellow-900/50 text-yellow-300 border-yellow-800/40',
};

export function ExternalUpstreamTab({ status, setStatus }: ExternalUpstreamTabProps) {
  const [config, setConfig] = useState<ExternalUpstream>({
    provider: 'openai',
    api_key: '',
    base_url: '',
  });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  // Fetch external upstream config on mount
  useEffect(() => {
    const fetchConfig = async () => {
      try {
        setLoading(true);
        const data = await getExternalUpstream();
        setConfig(data);
      } catch (e) {
        setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to fetch external upstream config' });
      } finally {
        setLoading(false);
      }
    };
    fetchConfig();
  }, [setStatus]);

  const handleProviderChange = (provider: Provider) => {
    setConfig({
      ...config,
      provider,
      base_url: PROVIDER_DEFAULTS[provider],
    });
    setStatus(null);
  };

  const handleApiKeyChange = (apiKey: string) => {
    setConfig({ ...config, api_key: apiKey });
    setStatus(null);
  };

  const handleBaseUrlChange = (baseUrl: string) => {
    setConfig({ ...config, base_url: baseUrl });
    setStatus(null);
  };

  const handleSave = async () => {
    try {
      setSaving(true);
      setStatus(null);
      const response = await updateExternalUpstream(config);
      setStatus({ 
        type: 'success', 
        message: 'External upstream configuration saved successfully',
        restartRequired: response.restart_required 
      });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save external upstream config' });
    } finally {
      setSaving(false);
    }
  };

  const handleClear = () => {
    setConfig({
      provider: 'openai',
      api_key: '',
      base_url: PROVIDER_DEFAULTS.openai,
    });
    setStatus(null);
  };

  if (loading) {
    return (
      <div class="flex items-center justify-center p-8">
        <div class="animate-spin w-6 h-6 border-2 border-blue-500 border-t-transparent rounded-full"></div>
        <span class="ml-3 text-gray-400">Loading external upstream configuration...</span>
      </div>
    );
  }

  return (
    <div class="space-y-4">
      <div class="mb-4">
        <h3 class="text-white font-medium mb-2">External Upstream Token</h3>
        <p class="text-sm text-gray-400 mb-4">
          Configure the API token for external upstream requests. When configured, all external requests will use this token instead of the client's authorization header.
        </p>
      </div>

      {/* Status Message */}
      {status && (
        <div
          class={`p-3 rounded-md flex items-center gap-2 ${
            status.type === 'success' ? 'bg-green-900/50 text-green-300 border border-green-800/40' : 'bg-red-900/50 text-red-300 border border-red-800/40'
          }`}
        >
          {status.type === 'success' ? (
            <svg class="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
            </svg>
          ) : (
            <svg class="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          )}
          <span class="text-sm">{status.message}</span>
          {status.restartRequired && (
            <span class="ml-auto text-xs bg-yellow-500/10 text-yellow-500 px-2 py-0.5 rounded">⚠️ Restart required</span>
          )}
        </div>
      )}

      {/* Provider Selection */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-2">Provider</label>
        <div class="grid grid-cols-4 gap-2">
          {PROVIDERS.map((provider) => (
            <button
              key={provider}
              onClick={() => handleProviderChange(provider)}
              class={`px-3 py-2 rounded-md text-sm font-medium transition-colors ${
                config.provider === provider
                  ? providerColors[provider]
                  : 'bg-gray-700 text-gray-300 hover:bg-gray-600 border border-gray-600'
              }`}
            >
              {provider.charAt(0).toUpperCase() + provider.slice(1)}
            </button>
          ))}
        </div>
      </div>

      {/* API Key */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">API Key</label>
        <input
          type="password"
          value={config.api_key || ''}
          onInput={(e) => handleApiKeyChange((e.target as HTMLInputElement).value)}
          class="w-full px-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
          placeholder="sk-..."
        />
        <p class="text-xs text-gray-500 mt-1">The API key for authenticating with the external upstream provider</p>
      </div>

      {/* Base URL */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Base URL (Optional)</label>
        <input
          type="text"
          value={config.base_url || ''}
          onInput={(e) => handleBaseUrlChange((e.target as HTMLInputElement).value)}
          class="w-full px-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
          placeholder={PROVIDER_DEFAULTS[config.provider as Provider]}
        />
        <p class="text-xs text-gray-500 mt-1">Custom base URL for the provider API (leave empty for default)</p>
      </div>

      {/* Action Buttons */}
      <div class="flex gap-3 pt-4">
        <button
          onClick={handleSave}
          disabled={saving}
          class="flex-1 bg-blue-600 hover:bg-blue-500 disabled:bg-gray-600 disabled:cursor-not-allowed text-white font-medium py-2 px-4 rounded-md transition-colors flex items-center justify-center gap-2"
        >
          {saving ? (
            <>
              <div class="animate-spin w-4 h-4 border-2 border-white border-t-transparent rounded-full"></div>
              <span>Saving...</span>
            </>
          ) : (
            <>
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
              </svg>
              <span>Save Configuration</span>
            </>
          )}
        </button>
        <button
          onClick={handleClear}
          disabled={saving}
          class="px-6 py-2 bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:cursor-not-allowed text-gray-300 font-medium rounded-md transition-colors"
        >
          Clear
        </button>
      </div>
    </div>
  );
}
