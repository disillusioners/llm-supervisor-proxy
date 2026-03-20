import { FunctionComponent } from 'preact';
import { route } from 'preact-router';
import { useVersion, useRam } from '../hooks/useApi';

// Format bytes to human-readable string (e.g., "45.2 MB" or "1.2 GB")
function formatRam(mb: number): string {
  if (mb >= 1024) {
    return (mb / 1024).toFixed(1) + ' GB';
  }
  return mb.toFixed(1) + ' MB';
}

const Header: FunctionComponent = () => {
  const { version } = useVersion();
  const { allocMB } = useRam();

  const handleOpenSettings = () => {
    route('/ui/settings');
  };

  return (
    <header class="bg-gray-800 border-b border-gray-700 p-4 flex justify-between items-center shadow-md z-10 shrink-0">
      {/* Left side: Logo + Title */}
      <div class="flex items-center space-x-3">
        <div class="p-2 bg-blue-600 rounded-lg">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-6 w-6 text-white"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M19.428 15.428a2 2 0 00-1.022-.547l-2.384-.477a6 6 0 00-3.86.517l-.318.158a6 6 0 01-3.86.517L6.05 15.21a2 2 0 00-1.806.547M8 4h8l-1 1v5.172a2 2 0 00.586 1.414l5 5c1.26 1.26.367 3.414-1.415 3.414H4.828c-1.782 0-2.674-2.154-1.414-3.414l5-5A2 2 0 009 10.172V5L8 4z"
            />
          </svg>
        </div>
        <h1 class="text-xl font-bold tracking-wide">LLM Supervisor Proxy</h1>
      </div>

      {/* Right side: Version + RAM + System Active indicator + Settings button */}
      <div class="text-sm text-gray-400 flex items-center space-x-4">
        <span class="px-2 py-1 bg-gray-700 rounded text-xs font-mono text-gray-300">v{version}</span>
        <span class="px-2 py-1 bg-gray-700 rounded text-xs font-mono text-gray-300">{formatRam(allocMB)}</span>
        <div class="flex items-center space-x-2">
          <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
          <span>System Active</span>
        </div>
        <button
          onClick={handleOpenSettings}
          class="text-gray-400 hover:text-white transition-colors"
          title="Settings"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-6 w-6"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
            />
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
            />
          </svg>
        </button>
      </div>
    </header>
  );
};

export default Header;
