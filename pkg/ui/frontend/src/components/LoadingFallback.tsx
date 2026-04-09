import { ComponentType } from 'preact';

export const LoadingFallback: ComponentType = () => {
  return (
    <div class="flex items-center justify-center h-full bg-gray-900">
      <div class="flex flex-col items-center space-y-4">
        <div class="w-10 h-10 border-4 border-gray-700 border-t-blue-500 rounded-full animate-spin"></div>
        <span class="text-gray-400 text-sm">Loading...</span>
      </div>
    </div>
  );
};
