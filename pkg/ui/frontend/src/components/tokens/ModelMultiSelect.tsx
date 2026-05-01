import { useState, useRef, useEffect } from 'preact/hooks';

interface ModelMultiSelectProps {
  models: { name: string; id: string; [key: string]: any }[];
  selected: string[];  // Currently selected model names
  onChange: (selected: string[]) => void;
  disabled?: boolean;
}

export function ModelMultiSelect({ models, selected, onChange, disabled }: ModelMultiSelectProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
        setSearchQuery('');
      }
    };

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isOpen]);

  // Filter models based on search query
  const filteredModels = models.filter(model =>
    model.name.toLowerCase().includes(searchQuery.toLowerCase())
  );

  // Check if a model is selected
  const isModelSelected = (modelName: string) => selected.includes(modelName);

  // Toggle a model's selection
  const toggleModel = (modelName: string) => {
    if (isModelSelected(modelName)) {
      onChange(selected.filter(name => name !== modelName));
    } else {
      onChange([...selected, modelName]);
    }
  };

  // Select all filtered models
  const selectAll = () => {
    const allFilteredNames = filteredModels.map(m => m.name);
    const newSelected = new Set([...selected, ...allFilteredNames]);
    onChange(Array.from(newSelected));
  };

  // Clear selections for filtered models only
  const clearAll = () => {
    const filteredNames = new Set(filteredModels.map(m => m.name));
    onChange(selected.filter(name => !filteredNames.has(name)));
  };

  // Handle toggle all (select/deselect only filtered models)
  const handleToggleAll = () => {
    const filteredNames = new Set(filteredModels.map(m => m.name));
    const filteredSelected = selected.filter(name => filteredNames.has(name));

    if (filteredSelected.length === 0) {
      // Select all filtered models
      const newSelected = new Set([...selected, ...filteredModels.map(m => m.name)]);
      onChange(Array.from(newSelected));
    } else {
      // Deselect all filtered models
      onChange(selected.filter(name => !filteredNames.has(name)));
    }
  };

  // Get display text
  const getDisplayText = () => {
    if (selected.length === 0) {
      return 'All models';
    }
    return `${selected.length} model${selected.length > 1 ? 's' : ''} selected`;
  };

  // Check if all filtered models are selected
  const allFilteredSelected = filteredModels.length > 0 && filteredModels.every(m => selected.includes(m.name));

  return (
    <div ref={containerRef} class="relative">
      {/* Trigger Button */}
      <button
        type="button"
        onClick={() => !disabled && setIsOpen(!isOpen)}
        disabled={disabled}
        class={`w-full flex items-center justify-between px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-sm transition-shadow focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent ${
          disabled ? 'opacity-50 cursor-not-allowed' : 'hover:border-gray-500'
        }`}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
      >
        <span class={selected.length === 0 ? 'text-gray-400' : 'text-gray-200'}>
          {getDisplayText()}
        </span>
        <svg
          class={`w-4 h-4 text-gray-400 transition-transform ${isOpen ? 'rotate-180' : ''}`}
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div class="absolute z-50 mt-1 w-full bg-gray-800 border border-gray-600 rounded-md shadow-lg max-h-64 flex flex-col">
          {/* Search Input */}
          <div class="p-2 border-b border-gray-700">
            <input
              type="text"
              value={searchQuery}
              onInput={(e) => setSearchQuery((e.target as HTMLInputElement).value)}
              placeholder="Search models..."
              class="w-full px-2 py-1.5 bg-gray-900 border border-gray-700 rounded text-sm text-white placeholder-gray-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              aria-label="Search models"
            />
          </div>

          {/* Select All / Clear All */}
          <div class="flex items-center justify-between px-2 py-1.5 border-b border-gray-700">
            <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
              <input
                type="checkbox"
                checked={allFilteredSelected}
                onChange={handleToggleAll}
                class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
              />
              <span>{allFilteredSelected ? 'Deselect all' : 'Select all'}</span>
            </label>
            <div class="flex gap-2">
              <button
                type="button"
                onClick={selectAll}
                class="text-xs text-blue-400 hover:text-blue-300"
              >
                All
              </button>
              <button
                type="button"
                onClick={clearAll}
                class="text-xs text-gray-400 hover:text-gray-300"
              >
                None
              </button>
            </div>
          </div>

          {/* Model List */}
          <div class="flex-1 overflow-y-auto">
            {filteredModels.length === 0 ? (
              <div class="px-3 py-2 text-sm text-gray-500 text-center">
                No models found
              </div>
            ) : (
              <ul class="py-1" role="listbox">
                {filteredModels.map((model) => {
                  const isSelected = isModelSelected(model.name);
                  return (
                    <li key={model.id}>
                      <label class="flex items-center gap-2 px-3 py-1.5 hover:bg-gray-700/50 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={isSelected}
                          onChange={() => toggleModel(model.name)}
                          class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                        />
                        <span class="text-sm text-gray-200 truncate" title={model.name}>
                          {model.name}
                        </span>
                      </label>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {/* Footer hint */}
          <div class="px-3 py-2 border-t border-gray-700 bg-gray-800/50 rounded-b-md">
            <p class="text-xs text-gray-500">
              {selected.length === 0
                ? 'All models are allowed'
                : `${selected.length} of ${models.length} models selected`
              }
            </p>
          </div>
        </div>
      )}
    </div>
  );
}
