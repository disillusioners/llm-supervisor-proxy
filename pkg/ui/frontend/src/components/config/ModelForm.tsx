import { useState, useEffect } from 'preact/hooks';
import type { Model, Credential } from '../../types';
import { getCredentials, useProviders } from '../../hooks/useApi';

// Peak hour time validation (uses UTC)
function isPeakHourActive(start: string, end: string, offset: string): boolean {
  if (!start || !end) return false;
  
  const now = new Date();
  const utcMinutes = now.getUTCHours() * 60 + now.getUTCMinutes();
  
  // Convert offset to minutes
  const offsetValue = parseFloat(offset) || 0;
  const offsetMinutes = Math.round(offsetValue * 60);
  
  // Normalize current time with offset to 0-1439 range
  const currentMinutes = ((utcMinutes + offsetMinutes) % 1440 + 1440) % 1440;
  
  // Convert start/end to minutes
  const [startH, startM] = start.split(':').map(Number);
  const [endH, endM] = end.split(':').map(Number);
  const startMinutes = startH * 60 + startM;
  const endMinutes = endH * 60 + endM;
  
  // Handle overnight wrap (e.g., 22:00 - 06:00)
  if (startMinutes > endMinutes) {
    return currentMinutes >= startMinutes || currentMinutes < endMinutes;
  }
  
  // Normal range (e.g., 09:00 - 17:00) - half-open interval [start, end)
  return currentMinutes >= startMinutes && currentMinutes < endMinutes;
}

// Timezone offsets from -12 to +14, plus half-hour offsets
const TIMEZONE_OFFSETS = [
  { value: '-12', label: 'UTC-12' },
  { value: '-11', label: 'UTC-11' },
  { value: '-10', label: 'UTC-10' },
  { value: '-9.5', label: 'UTC-9:30' },
  { value: '-9', label: 'UTC-9' },
  { value: '-8', label: 'UTC-8' },
  { value: '-7', label: 'UTC-7' },
  { value: '-6', label: 'UTC-6' },
  { value: '-5.5', label: 'UTC-5:30' },
  { value: '-5', label: 'UTC-5' },
  { value: '-4', label: 'UTC-4' },
  { value: '-3.5', label: 'UTC-3:30' },
  { value: '-3', label: 'UTC-3' },
  { value: '-2', label: 'UTC-2' },
  { value: '-1', label: 'UTC-1' },
  { value: '+0', label: 'UTC+0' },
  { value: '+1', label: 'UTC+1' },
  { value: '+2', label: 'UTC+2' },
  { value: '+3', label: 'UTC+3' },
  { value: '+3.5', label: 'UTC+3:30' },
  { value: '+4', label: 'UTC+4' },
  { value: '+4.5', label: 'UTC+4:30' },
  { value: '+5', label: 'UTC+5' },
  { value: '+5.5', label: 'UTC+5:30' },
  { value: '+5.75', label: 'UTC+5:45' },
  { value: '+6', label: 'UTC+6' },
  { value: '+6.5', label: 'UTC+6:30' },
  { value: '+7', label: 'UTC+7' },
  { value: '+8', label: 'UTC+8' },
  { value: '+9', label: 'UTC+9' },
  { value: '+9.5', label: 'UTC+9:30' },
  { value: '+10', label: 'UTC+10' },
  { value: '+10.5', label: 'UTC+10:30' },
  { value: '+11', label: 'UTC+11' },
  { value: '+12', label: 'UTC+12' },
  { value: '+13', label: 'UTC+13' },
  { value: '+14', label: 'UTC+14' },
];

interface ModelFormProps {
  mode: 'add' | 'edit';
  initialData?: Model;
  onSave: (data: {
    id: string;
    name: string;
    fallback_chain: string[];
    truncate_params: string[];
    internal?: boolean;
    credential_id?: string;
    internal_api_key?: string;
    internal_base_url?: string;
    internal_model?: string;
    release_stream_chunk_deadline?: string;
    peak_hour_enabled?: boolean;
    peak_hour_start?: string;
    peak_hour_end?: string;
    peak_hour_timezone?: string;
    peak_hour_model?: string;
  }) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onNavigateToCredentials?: () => void;
}

export function ModelForm({ mode, initialData, onSave, onCancel, onStatus, onNavigateToCredentials }: ModelFormProps) {
  // Fetch providers from API (single source of truth)
  const { providers } = useProviders();

  // Get provider display name from API data
  const getProviderName = (providerType: string): string => {
    const provider = providers.find(p => p.type === providerType);
    return provider?.name || providerType;
  };

  const [formData, setFormData] = useState({
    id: '',
    name: '',
    fallback_chain: '',
    truncate_params: '',
    internal: false,
    credential_id: '',
    internal_api_key: '',
    internal_base_url: '',
    internal_model: '',
    release_stream_chunk_deadline: '',
    peak_hour_enabled: false,
    peak_hour_start: '',
    peak_hour_end: '',
    peak_hour_timezone: '+0',
    peak_hour_model: '',
  });
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [loadingCredentials, setLoadingCredentials] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [peakHourErrors, setPeakHourErrors] = useState<string[]>([]);
  const [currentTimeTick, setCurrentTimeTick] = useState(Date.now());

  // Fetch credentials on mount
  useEffect(() => {
    const fetchCredentials = async () => {
      setLoadingCredentials(true);
      try {
        const data = await getCredentials();
        setCredentials(data || []);
      } catch (e) {
        console.error('Failed to fetch credentials:', e);
      } finally {
        setLoadingCredentials(false);
      }
    };
    fetchCredentials();
  }, []);

  // Time ticker for peak hour status (updates every 60 seconds)
  useEffect(() => {
    const interval = setInterval(() => {
      setCurrentTimeTick(Date.now());
    }, 60000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    if (mode === 'edit' && initialData) {
      let truncateParams = (initialData.truncate_params ?? []).join(', ');
      if (truncateParams.trim() === '' && initialData.id.toLowerCase().includes('glm')) {
        truncateParams = 'max_completion_tokens, store';
      }

      setFormData({
        id: initialData.id,
        name: initialData.name,
        fallback_chain: (initialData.fallback_chain ?? []).join(', '),
        truncate_params: truncateParams,
        internal: initialData.internal ?? false,
        credential_id: initialData.credential_id ?? '',
        internal_api_key: '',
        internal_base_url: initialData.internal_base_url || '',
        internal_model: initialData.internal_model ?? '',
        release_stream_chunk_deadline: initialData.release_stream_chunk_deadline ?? '',
        peak_hour_enabled: initialData.peak_hour_enabled ?? false,
        peak_hour_start: initialData.peak_hour_start ?? '',
        peak_hour_end: initialData.peak_hour_end ?? '',
        peak_hour_timezone: initialData.peak_hour_timezone ?? '+0',
        peak_hour_model: initialData.peak_hour_model ?? '',
      });
    } else if (mode === 'add') {
      setFormData({
        id: '',
        name: '',
        fallback_chain: '',
        truncate_params: '',
        internal: false,
        credential_id: '',
        internal_api_key: '',
        internal_base_url: '',
        internal_model: '',
        release_stream_chunk_deadline: '',
        peak_hour_enabled: false,
        peak_hour_start: '',
        peak_hour_end: '',
        peak_hour_timezone: '+0',
        peak_hour_model: '',
      });
    }
  }, [mode, initialData]);

  const handleInputChange = (field: string, value: string | boolean) => {
    setFormData(prev => {
      const updated = { ...prev, [field]: value };
      setPeakHourErrors([]);
      
      // Auto-fill truncate_params for GLM models when adding
      if (field === 'id' && mode === 'add' && typeof value === 'string') {
        if (value.toLowerCase().includes('glm') && prev.truncate_params.trim() === '') {
          updated.truncate_params = 'max_completion_tokens, store';
        }
      }
      
      // Auto-fill truncate_params for GLM when name changes
      if (field === 'name' && typeof value === 'string') {
        if (value.toLowerCase().includes('glm') && prev.truncate_params.trim() === '') {
          updated.truncate_params = 'max_completion_tokens, store';
        }
      }
      
      // Clear base URL override when credential changes
      if (field === 'credential_id') {
        updated.internal_base_url = '';
      }

      // Clear credential when toggling internal off
      if (field === 'internal' && value === false) {
        updated.credential_id = '';
        updated.internal_api_key = '';
        // Clear all peak hour fields when internal upstream is turned off
        updated.peak_hour_enabled = false;
        updated.peak_hour_start = '';
        updated.peak_hour_end = '';
        updated.peak_hour_timezone = '+0';
        updated.peak_hour_model = '';
      }
      
      // Clear peak hour fields when peak_hour_enabled is toggled off
      if (field === 'peak_hour_enabled' && value === false) {
        updated.peak_hour_start = '';
        updated.peak_hour_end = '';
        updated.peak_hour_timezone = '+0';
        updated.peak_hour_model = '';
      }
      
      return updated;
    });
  };

  const handleSubmit = async () => {
    try {
      setSaving(true);
      setPeakHourErrors([]);
      onStatus(null);
      
      const fallback = formData.fallback_chain.split(',').map(s => s.trim()).filter(Boolean);
      const truncate = formData.truncate_params.split(',').map(s => s.trim()).filter(Boolean);

      // Validate peak hour fields when enabled
      if (formData.internal && formData.peak_hour_enabled) {
        const errors: string[] = [];
        if (!formData.peak_hour_start) errors.push('Peak Hour Start is required');
        if (!formData.peak_hour_end) errors.push('Peak Hour End is required');
        if (!formData.peak_hour_model) errors.push('Peak Hour Model Name is required');
        
        if (errors.length > 0) {
          setPeakHourErrors(errors);
          throw new Error('Please fill in all required peak hour fields');
        }
      }

      if (mode === 'add') {
        if (!formData.id || !formData.name) {
          throw new Error('ID and Name are required');
        }
      } else {
        if (!formData.name) {
          throw new Error('Name is required');
        }
      }

      await onSave({
        id: formData.id,
        name: formData.name,
        fallback_chain: fallback,
        truncate_params: truncate,
        internal: formData.internal || undefined,
        credential_id: formData.internal && formData.credential_id ? formData.credential_id : undefined,
        internal_api_key: formData.internal && formData.internal_api_key ? formData.internal_api_key : undefined,
        internal_base_url: formData.internal && formData.internal_base_url ? formData.internal_base_url : undefined,
        internal_model: formData.internal && formData.internal_model ? formData.internal_model : undefined,
        release_stream_chunk_deadline: formData.release_stream_chunk_deadline || undefined,
        // Peak hour fields only included when internal is true
        peak_hour_enabled: formData.internal ? formData.peak_hour_enabled : undefined,
        peak_hour_start: formData.internal && formData.peak_hour_enabled ? formData.peak_hour_start : undefined,
        peak_hour_end: formData.internal && formData.peak_hour_enabled ? formData.peak_hour_end : undefined,
        peak_hour_timezone: formData.internal && formData.peak_hour_enabled ? formData.peak_hour_timezone : undefined,
        peak_hour_model: formData.internal && formData.peak_hour_enabled ? formData.peak_hour_model : undefined,
      });
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save model' });
    } finally {
      setSaving(false);
    }
  };

  const handleTestConnection = async () => {
    try {
      setTesting(true);
      onStatus(null);

      // Get provider from selected credential
      const selectedCred = credentials.find(c => c.id === formData.credential_id);
      const baseUrl = formData.internal_base_url || selectedCred?.base_url;

      const payload: Record<string, string | undefined> = {
        credential_id: formData.credential_id || undefined,
        api_key: formData.internal_api_key || undefined,
        internal_base_url: baseUrl,
        internal_model: formData.internal_model,
      };

      // Note: We don't pass model_id anymore because we want to test with the
      // current form data, not the saved model data. This allows testing
      // internal config before saving the model.

      const response = await fetch('/fe/api/models/test', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
      });

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(errorData.error || `HTTP ${response.status}: Connection test failed`);
      }

      onStatus({ type: 'success', message: 'Connection test successful!' });
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Connection test failed' });
    } finally {
      setTesting(false);
    }
  };

  // Can test if: need credential (or API key) and model name
  const canTestConnection = formData.internal_model && 
    (mode === 'edit' || formData.credential_id || formData.internal_api_key);

  const isValid = mode === 'add' 
    ? formData.id.trim() !== '' && formData.name.trim() !== ''
    : formData.name.trim() !== '';

  // Get the currently selected credential
  const selectedCredential = credentials.find(cred => cred.id === formData.credential_id);

  // Compute the default base URL (from credential)
  const defaultBaseUrl = selectedCredential?.base_url || 'Provider default';

  // Get provider display name from selected credential
  const selectedProvider = selectedCredential?.provider;

  // Compute live status (depends on currentTimeTick to update every minute)
  const peakHourActive = formData.peak_hour_start && formData.peak_hour_end && formData.peak_hour_model
    ? isPeakHourActive(formData.peak_hour_start, formData.peak_hour_end, formData.peak_hour_timezone)
    : false;

  return (
    <div class="bg-gray-700/50 rounded-lg p-5 border border-gray-600">
      <h3 class="text-lg font-medium text-white mb-4">
        {mode === 'add' ? 'Add New Model' : 'Edit Model'}
      </h3>
      <div class="space-y-4">
        {mode === 'add' && (
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Model ID <span class="text-red-400">*</span></label>
            <input
              type="text"
              value={formData.id}
              onInput={(e) => handleInputChange('id', (e.target as HTMLInputElement).value)}
              class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., gpt-4"
            />
          </div>
        )}
        
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Display Name <span class="text-red-400">*</span></label>
          <input
            type="text"
            value={formData.name}
            onInput={(e) => handleInputChange('name', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., GPT-4"
          />
        </div>
        
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Fallback Chain (comma-separated IDs)</label>
          <input
            type="text"
            value={formData.fallback_chain}
            onInput={(e) => handleInputChange('fallback_chain', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., gpt-3.5-turbo, claude-2"
          />
          <p class="text-xs text-gray-400 mt-1">Leave empty for no fallbacks.</p>
        </div>
        
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Strip Params <span class="text-gray-500">(optional)</span></label>
          <input
            type="text"
            value={formData.truncate_params}
            onInput={(e) => handleInputChange('truncate_params', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-shadow"
            placeholder="e.g., max_completion_tokens, store"
          />
          <p class="text-xs text-gray-400 mt-1">Parameters to remove before forwarding to this model (e.g. unsupported OpenAI params).</p>
        </div>

        {/* STREAM CHUNK DEADLINE: Hidden - feature not used anymore, can be re-enabled later
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Stream Chunk Deadline <span class="text-gray-500">(optional)</span></label>
          <div class="relative">
            <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            </span>
            <input
              type="text"
              value={formData.release_stream_chunk_deadline}
              onInput={(e) => handleInputChange('release_stream_chunk_deadline', (e.target as HTMLInputElement).value)}
              class="w-full pl-10 pr-4 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., 1m30s, 2m"
            />
          </div>
          <p class="text-xs text-gray-400 mt-1">
            Time limit for buffering stream chunks before releasing to client. Impro responsiveness for slow connections. Leave empty for no deadline.
          </p>
        </div>
        */}

        {/* Internal Upstream Section */}
        <div class="border-t border-gray-600 pt-4">
          <label class="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={formData.internal}
              onInput={(e) => handleInputChange('internal', (e.target as HTMLInputElement).checked)}
              class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
            />
            <span class="text-gray-300 font-medium">Internal Upstream</span>
            <span class="text-xs text-gray-500">(use custom provider instead of global upstream)</span>
          </label>
        </div>

        {formData.internal && (
          <div class="bg-gray-800/50 rounded-md p-4 space-y-3 border border-gray-600/50">
            <div>
              <div class="flex items-center justify-between mb-1">
                <label class="block text-sm font-medium text-gray-300">Credential</label>
                {onNavigateToCredentials && (
                  <button
                    type="button"
                    onClick={onNavigateToCredentials}
                    class="text-xs text-blue-400 hover:text-blue-300 transition-colors"
                  >
                    Manage Credentials
                  </button>
                )}
              </div>
              {loadingCredentials ? (
                <div class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-gray-400 text-sm">
                  Loading credentials...
                </div>
              ) : (
                <select
                  value={formData.credential_id}
                  onChange={(e) => handleInputChange('credential_id', (e.target as HTMLSelectElement).value)}
                  class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                >
                  <option value="">Select a credential</option>
                  {credentials.map((cred) => (
                    <option key={cred.id} value={cred.id}>
                      {cred.id} ({cred.provider || 'unknown'})
                    </option>
                  ))}
                </select>
              )}
              {credentials.length === 0 && !loadingCredentials && (
                <p class="text-xs text-gray-400 mt-1">
                  No credentials found. 
                  {onNavigateToCredentials && (
                    <button
                      type="button"
                      onClick={onNavigateToCredentials}
                      class="text-blue-400 hover:text-blue-300 ml-1"
                    >
                      Add a credential
                    </button>
                  )}
                </p>
              )}
              {selectedProvider && (
                <div class="mt-2 flex items-center gap-2">
                  <span class="text-xs text-gray-400">Provider:</span>
                  <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-900/50 text-blue-300 border border-blue-700">
                    {getProviderName(selectedProvider)}
                  </span>
                </div>
              )}
              {selectedCredential?.base_url && (
                <p class="text-xs text-gray-500 mt-1">
                  Credential base URL: <span class="text-gray-400">{selectedCredential.base_url}</span>
                </p>
              )}
            </div>

            {formData.credential_id && (
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">
                  API Key Override <span class="text-gray-500">(optional)</span>
                </label>
                <input
                  type="password"
                  value={formData.internal_api_key}
                  onInput={(e) => handleInputChange('internal_api_key', (e.target as HTMLInputElement).value)}
                  class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                  placeholder="Overrides the credential's API key if set"
                />
                <p class="text-xs text-gray-400 mt-1">Overrides the credential's API key if set</p>
              </div>
            )}

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">Base URL Override <span class="text-gray-500">(optional)</span></label>
              <input
                type="text"
                value={formData.internal_base_url}
                onInput={(e) => handleInputChange('internal_base_url', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={defaultBaseUrl}
              />
              <p class="text-xs text-gray-400 mt-1">Default: {defaultBaseUrl}</p>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">Upstream Model Name <span class="text-red-400">*</span></label>
              <input
                type="text"
                value={formData.internal_model}
                onInput={(e) => handleInputChange('internal_model', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={selectedProvider === 'openai' ? 'gpt-4o' : selectedProvider === 'zhipu' ? 'glm-4' : 'gpt-4'}
              />
              <p class="text-xs text-gray-400 mt-1">Actual model name at the provider (e.g., "gpt-4o" for OpenAI)</p>
            </div>

            {/* Peak Hour Auto-Switch Section */}
            <div class="border-t border-gray-600/50 pt-3">
              <div class="bg-gray-800/50 rounded-md p-4 space-y-3 border border-gray-600/50">
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={formData.peak_hour_enabled}
                    onInput={(e) => handleInputChange('peak_hour_enabled', (e.target as HTMLInputElement).checked)}
                    class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-amber-500 focus:ring-amber-500 focus:ring-offset-gray-800"
                  />
                  <span class="text-gray-300 font-medium">Enable Peak Hour Switch</span>
                </label>

                {formData.peak_hour_enabled && (
                  <div class="space-y-3 pl-6">
                    {/* Peak Hour Errors */}
                    {peakHourErrors.length > 0 && (
                      <div class="bg-red-900/30 border border-red-700/50 rounded-md p-2">
                        {peakHourErrors.map((err, i) => (
                          <p key={i} class="text-xs text-red-300">{err}</p>
                        ))}
                      </div>
                    )}

                    {/* Time Window Inputs */}
                    <div class="flex gap-4">
                      <div class="flex-1">
                        <label class="block text-sm font-medium text-gray-300 mb-1">
                          Peak Hour Start <span class="text-red-400">*</span>
                        </label>
                        <input
                          type="time"
                          value={formData.peak_hour_start}
                          onInput={(e) => handleInputChange('peak_hour_start', (e.target as HTMLInputElement).value)}
                          class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent transition-shadow"
                        />
                      </div>
                      <div class="flex-1">
                        <label class="block text-sm font-medium text-gray-300 mb-1">
                          Peak Hour End <span class="text-red-400">*</span>
                        </label>
                        <input
                          type="time"
                          value={formData.peak_hour_end}
                          onInput={(e) => handleInputChange('peak_hour_end', (e.target as HTMLInputElement).value)}
                          class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent transition-shadow"
                        />
                      </div>
                    </div>
                    <p class="text-xs text-gray-400 mt-1">Times in local timezone (with selected offset from UTC)</p>

                    {/* Timezone Selector */}
                    <div>
                      <label class="block text-sm font-medium text-gray-300 mb-1">
                        Timezone Offset
                      </label>
                      <select
                        value={formData.peak_hour_timezone}
                        onChange={(e) => handleInputChange('peak_hour_timezone', (e.target as HTMLSelectElement).value)}
                        class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent transition-shadow"
                      >
                        {TIMEZONE_OFFSETS.map(tz => (
                          <option key={tz.value} value={tz.value}>{tz.label}</option>
                        ))}
                      </select>
                      <p class="text-xs text-gray-400 mt-1">Offset from UTC (e.g., +8 for China, +5.5 for India)</p>
                    </div>

                    {/* Peak Hour Model Name */}
                    <div>
                      <label class="block text-sm font-medium text-gray-300 mb-1">
                        Peak Hour Model Name <span class="text-red-400">*</span>
                      </label>
                      <input
                        type="text"
                        value={formData.peak_hour_model}
                        onInput={(e) => handleInputChange('peak_hour_model', (e.target as HTMLInputElement).value)}
                        class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent transition-shadow"
                        placeholder="gpt-4o-mini"
                      />
                      <p class="text-xs text-gray-400 mt-1">Model to use during peak hours (e.g., faster/cheaper model)</p>
                    </div>

                    {/* Live Status Indicator */}
                    {formData.peak_hour_start && formData.peak_hour_end && formData.peak_hour_model && (
                      <div class={`rounded-md px-3 py-2 text-xs font-medium ${
                        peakHourActive
                          ? 'bg-red-900/40 border border-red-700/50 text-red-200'
                          : 'bg-green-900/40 border border-green-700/50 text-green-200'
                      }`}>
                        <div class="flex items-center gap-2">
                          <span class={`w-2 h-2 rounded-full ${
                            peakHourActive
                              ? 'bg-red-500 animate-pulse'
                              : 'bg-green-500'
                          }`}></span>
                          <span>
                            Currently using: <span class="font-semibold">{
                              peakHourActive
                                ? formData.peak_hour_model
                                : formData.internal_model
                            }</span> ({
                              peakHourActive
                                ? 'peak hour'
                                : 'off-peak'
                            })
                          </span>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>

            <div class="pt-2">
              <button
                onClick={handleTestConnection}
                class="px-4 py-2 bg-green-600 hover:bg-green-500 text-white rounded-md transition-colors text-sm font-medium"
                disabled={!canTestConnection || testing}
              >
                {testing ? 'Testing...' : 'Test Connection'}
              </button>
            </div>
          </div>
        )}

        <div class="flex justify-end gap-3 pt-2">
          <button
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
          >
            Cancel
          </button>
          <button
            onClick={handleSubmit}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium"
            disabled={!isValid || saving}
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Model' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  );
}
