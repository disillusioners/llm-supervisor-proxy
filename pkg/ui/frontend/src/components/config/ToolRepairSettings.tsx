import { useState, useEffect } from 'preact/hooks';
import type { ToolRepairConfig } from '../../types';

interface ToolRepairSettingsProps {
  config: ToolRepairConfig | null;
  onApply: (config: ToolRepairConfig) => Promise<void>;
  status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}

const AVAILABLE_STRATEGIES = [
  { id: 'extract_json', label: 'Extract JSON', description: 'Extracts JSON from mixed text content' },
  { id: 'library_repair', label: 'Library Repair', description: 'Uses jsonrepair library to fix common issues' },
  { id: 'remove_reasoning', label: 'Remove Reasoning', description: 'Strips reasoning leakage patterns from JSON' },
];

export function ToolRepairSettings({
  config,
  onApply,
  setStatus,
}: ToolRepairSettingsProps) {
  const [enabled, setEnabled] = useState(true);
  const [selectedStrategies, setSelectedStrategies] = useState<string[]>([]);
  const [maxArgumentsSize, setMaxArgumentsSize] = useState(10); // KB
  const [maxToolCallsPerResponse, setMaxToolCallsPerResponse] = useState(8);
  const [logOriginal, setLogOriginal] = useState(false);
  const [logRepaired, setLogRepaired] = useState(true);
  const [retryEnabled, setRetryEnabled] = useState(true);
  const [maxRetries, setMaxRetries] = useState(1);
  const [retryPrompt, setRetryPrompt] = useState('');
  const [maxRepairDuration, setMaxRepairDuration] = useState(500); // ms

  // Sync state when config changes
  useEffect(() => {
    if (config) {
      setEnabled(config.enabled);
      setSelectedStrategies(config.strategies || []);
      setMaxArgumentsSize(Math.floor((config.max_arguments_size || 10240) / 1024)); // Convert bytes to KB
      setMaxToolCallsPerResponse(config.max_tool_calls_per_response || 8);
      setLogOriginal(config.log_original || false);
      setLogRepaired(config.log_repaired ?? true);
      setRetryEnabled(config.retry_enabled ?? true);
      setMaxRetries(config.max_retries || 1);
      setRetryPrompt(config.retry_prompt || '');
      setMaxRepairDuration(config.max_repair_duration || 500);
    }
  }, [config]);

  const toggleStrategy = (strategyId: string) => {
    setSelectedStrategies(prev => {
      if (prev.includes(strategyId)) {
        return prev.filter(s => s !== strategyId);
      }
      return [...prev, strategyId];
    });
  };

  const moveStrategy = (index: number, direction: 'up' | 'down') => {
    const newIndex = direction === 'up' ? index - 1 : index + 1;
    if (newIndex < 0 || newIndex >= selectedStrategies.length) return;
    
    const newStrategies = [...selectedStrategies];
    [newStrategies[index], newStrategies[newIndex]] = [newStrategies[newIndex], newStrategies[index]];
    setSelectedStrategies(newStrategies);
  };

  const handleApply = async () => {
    try {
      setStatus(null);
      
      if (selectedStrategies.length === 0) {
        setStatus({ type: 'error', message: 'At least one strategy must be selected' });
        return;
      }

      await onApply({
        enabled,
        strategies: selectedStrategies,
        max_arguments_size: maxArgumentsSize * 1024, // Convert KB to bytes
        max_tool_calls_per_response: maxToolCallsPerResponse,
        log_original: logOriginal,
        log_repaired: logRepaired,
        retry_enabled: retryEnabled,
        max_retries: maxRetries,
        retry_prompt: retryPrompt,
        max_repair_duration: maxRepairDuration,
      });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update tool repair config' });
    }
  };

  return (
    <div class="space-y-4">
      {/* Enable Toggle */}
      <div class="flex gap-6">
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={enabled}
            onInput={(e) => setEnabled((e.target as HTMLInputElement).checked)}
            class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
          />
          <span class="text-gray-300">Enabled</span>
        </label>
      </div>

      {/* Info Box */}
      <div class="bg-blue-900/20 border border-blue-800/30 rounded-md p-3">
        <p class="text-sm text-blue-300">
          <strong>Tool Call Repair</strong> automatically fixes malformed JSON arguments in LLM tool calls.
          When LLMs return invalid JSON, this feature attempts to repair it before the tool is executed.
        </p>
      </div>

      {/* Strategies */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Repair Strategies</h4>
        <p class="text-xs text-gray-500 mb-3">Select and order the strategies to apply (in order)</p>
        
        {/* Selected Strategies (Ordered) */}
        <div class="mb-3 space-y-1">
          {selectedStrategies.map((strategyId, index) => {
            const strategy = AVAILABLE_STRATEGIES.find(s => s.id === strategyId);
            if (!strategy) return null;
            return (
              <div key={strategyId} class="flex items-center gap-2 bg-gray-700/50 rounded px-3 py-2">
                <span class="text-gray-500 text-xs w-5">{index + 1}</span>
                <span class="text-gray-200 flex-1">{strategy.label}</span>
                <button
                  onClick={() => moveStrategy(index, 'up')}
                  disabled={index === 0}
                  class="text-gray-400 hover:text-white disabled:opacity-30"
                  title="Move up"
                >
                  ↑
                </button>
                <button
                  onClick={() => moveStrategy(index, 'down')}
                  disabled={index === selectedStrategies.length - 1}
                  class="text-gray-400 hover:text-white disabled:opacity-30"
                  title="Move down"
                >
                  ↓
                </button>
                <button
                  onClick={() => toggleStrategy(strategyId)}
                  class="text-red-400 hover:text-red-300 ml-2"
                  title="Remove"
                >
                  ✕
                </button>
              </div>
            );
          })}
          {selectedStrategies.length === 0 && (
            <p class="text-gray-500 text-sm italic">No strategies selected</p>
          )}
        </div>

        {/* Available Strategies to Add */}
        <div class="space-y-1">
          <p class="text-xs text-gray-500 mb-1">Add strategy:</p>
          {AVAILABLE_STRATEGIES
            .filter(s => !selectedStrategies.includes(s.id))
            .map(strategy => (
              <button
                key={strategy.id}
                onClick={() => toggleStrategy(strategy.id)}
                class="w-full text-left px-3 py-2 bg-gray-800 hover:bg-gray-700 rounded text-sm text-gray-300 transition-colors"
              >
                <span class="font-medium">{strategy.label}</span>
                <span class="text-gray-500 ml-2">- {strategy.description}</span>
              </button>
            ))}
        </div>
      </div>

      {/* Size Limits */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Size Limits</h4>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Max Arguments Size (KB)</label>
            <input
              type="number"
              value={maxArgumentsSize}
              onInput={(e) => setMaxArgumentsSize(parseInt((e.target as HTMLInputElement).value) || 10)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="1"
              max="100"
            />
            <p class="text-xs text-gray-500 mt-1">Maximum size of tool arguments</p>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Max Tool Calls per Response</label>
            <input
              type="number"
              value={maxToolCallsPerResponse}
              onInput={(e) => setMaxToolCallsPerResponse(parseInt((e.target as HTMLInputElement).value) || 8)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="1"
              max="50"
            />
            <p class="text-xs text-gray-500 mt-1">Maximum tool calls to process</p>
          </div>
        </div>
      </div>

      {/* Logging Options */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Logging</h4>
        <div class="flex gap-6">
          <label class="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={logOriginal}
              onInput={(e) => setLogOriginal((e.target as HTMLInputElement).checked)}
              class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
            />
            <span class="text-gray-300">Log Original</span>
            <span class="text-xs text-gray-500">(log malformed JSON)</span>
          </label>
          <label class="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={logRepaired}
              onInput={(e) => setLogRepaired((e.target as HTMLInputElement).checked)}
              class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
            />
            <span class="text-gray-300">Log Repaired</span>
            <span class="text-xs text-gray-500">(log repaired JSON)</span>
          </label>
        </div>
      </div>

      {/* Retry Options */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Retry Options</h4>
        <div class="flex gap-6 mb-3">
          <label class="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={retryEnabled}
              onInput={(e) => setRetryEnabled((e.target as HTMLInputElement).checked)}
              class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
            />
            <span class="text-gray-300">Retry Enabled</span>
            <span class="text-xs text-gray-500">(retry with prompt injection)</span>
          </label>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Max Retries</label>
            <input
              type="number"
              value={maxRetries}
              onInput={(e) => setMaxRetries(parseInt((e.target as HTMLInputElement).value) || 1)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="0"
              max="5"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Max Repair Duration (ms)</label>
            <input
              type="number"
              value={maxRepairDuration}
              onInput={(e) => setMaxRepairDuration(parseInt((e.target as HTMLInputElement).value) || 500)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="100"
              max="5000"
              step="100"
            />
          </div>
        </div>
        <div class="mt-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Retry Prompt</label>
          <textarea
            value={retryPrompt}
            onInput={(e) => setRetryPrompt((e.target as HTMLTextAreaElement).value)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            rows={2}
            placeholder="The previous tool call arguments were invalid JSON. Return only valid JSON matching the tool schema."
          />
          <p class="text-xs text-gray-500 mt-1">Prompt injected when repair fails and retry is enabled</p>
        </div>
      </div>

      {/* Apply Button */}
      <div class="pt-2">
        <button
          onClick={handleApply}
          class="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium py-2 px-4 rounded-md transition-colors shadow shadow-blue-900/20"
        >
          Apply Tool Repair Settings
        </button>
      </div>
    </div>
  );
}
