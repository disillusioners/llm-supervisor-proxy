import { useEffect, useState } from 'preact/hooks';

export interface ToastData {
  id: string;
  type: 'success' | 'error' | 'warning';
  message: string;
  restartRequired?: boolean;
}

interface ToastProps {
  toast: ToastData;
  onDismiss: (id: string) => void;
  autoDismissDelay?: number;
}

export function Toast({ toast, onDismiss, autoDismissDelay = 4000 }: ToastProps) {
  const [isVisible, setIsVisible] = useState(false);
  const [isLeaving, setIsLeaving] = useState(false);

  // Trigger enter animation on mount
  useEffect(() => {
    // Small delay to allow CSS transition to work
    requestAnimationFrame(() => {
      setIsVisible(true);
    });
  }, []);

  // Auto-dismiss for success and warning messages
  useEffect(() => {
    if (toast.type === 'success' || toast.type === 'warning') {
      const timer = setTimeout(() => {
        handleDismiss();
      }, autoDismissDelay);
      return () => clearTimeout(timer);
    }
  }, [toast.id, toast.type, autoDismissDelay]);

  const handleDismiss = () => {
    setIsLeaving(true);
    // Wait for exit animation to complete
    setTimeout(() => {
      onDismiss(toast.id);
    }, 300);
  };

  const getTypeStyles = () => {
    switch (toast.type) {
      case 'success':
        return {
          bg: 'bg-emerald-900/90',
          border: 'border-emerald-700/50',
          icon: 'text-emerald-400',
          text: 'text-emerald-100',
          iconPath: 'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
        };
      case 'error':
        return {
          bg: 'bg-red-900/90',
          border: 'border-red-700/50',
          icon: 'text-red-400',
          text: 'text-red-100',
          iconPath: 'M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
        };
      case 'warning':
        return {
          bg: 'bg-amber-900/90',
          border: 'border-amber-700/50',
          icon: 'text-amber-400',
          text: 'text-amber-100',
          iconPath: 'M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
        };
    }
  };

  const styles = getTypeStyles();

  return (
    <div
      role="alert"
      aria-live="polite"
      class={`
        flex items-start gap-3 p-4 rounded-lg border shadow-lg backdrop-blur-sm
        transition-all duration-300 ease-out
        ${styles.bg} ${styles.border}
        ${isVisible && !isLeaving 
          ? 'translate-x-0 opacity-100' 
          : 'translate-x-full opacity-0'
        }
      `}
      style={{ 
        // Override Tailwind's default transition for custom behavior
        transitionProperty: 'transform, opacity',
      }}
    >
      {/* Icon */}
      <svg 
        class={`w-5 h-5 flex-shrink-0 mt-0.5 ${styles.icon}`} 
        fill="none" 
        stroke="currentColor" 
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <path 
          stroke-linecap="round" 
          stroke-linejoin="round" 
          stroke-width="2" 
          d={styles.iconPath}
        />
      </svg>

      {/* Content */}
      <div class="flex-1 min-w-0">
        <p class={`font-medium ${styles.text}`}>{toast.message}</p>
        {toast.restartRequired && (
          <p class="mt-1.5 text-sm text-yellow-300 font-medium flex items-center gap-1.5">
            <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            Server restart required for changes to take effect
          </p>
        )}
      </div>

      {/* Dismiss button */}
      <button
        onClick={handleDismiss}
        class={`flex-shrink-0 p-1 rounded-md hover:bg-white/10 transition-colors ${styles.icon}`}
        aria-label="Dismiss notification"
      >
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
  );
}

// Container for managing multiple toasts
interface ToastContainerProps {
  toasts: ToastData[];
  onDismiss: (id: string) => void;
  autoDismissDelay?: number;
}

export function ToastContainer({ toasts, onDismiss, autoDismissDelay }: ToastContainerProps) {
  if (toasts.length === 0) return null;

  return (
    <div 
      class="fixed top-4 right-4 z-[100] flex flex-col gap-3 max-w-sm w-full pointer-events-none"
      role="region"
      aria-label="Notifications"
    >
      {toasts.map((toast) => (
        <div key={toast.id} class="pointer-events-auto">
          <Toast 
            toast={toast} 
            onDismiss={onDismiss} 
            autoDismissDelay={autoDismissDelay}
          />
        </div>
      ))}
    </div>
  );
}
