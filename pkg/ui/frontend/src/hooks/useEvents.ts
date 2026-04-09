import { useState, useEffect, useCallback, useRef, useMemo } from 'preact/hooks';
import type { Event } from '../types';

// Configuration constants
const MAX_EVENTS_PER_REQUEST = 500;
const MAX_REQUESTS_IN_MAP = 100;
const RECONNECT_BASE_DELAY = 1000; // 1 second
const RECONNECT_MAX_DELAY = 30000; // 30 seconds

// Connection state type
export type ConnectionState = 'connecting' | 'connected' | 'disconnected';

// Shared EventSource management to avoid redundant connections
let sharedEventSource: EventSource | null = null;
const subscribers = new Set<(data: Event) => void>();
const stateListeners = new Set<(state: ConnectionState) => void>();
let reconnectAttempts = 0;
let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
let isConnecting = false;

function notifyStateListeners(state: ConnectionState) {
  stateListeners.forEach((listener) => listener(state));
}

function createEventSource() {
  if (sharedEventSource) {
    sharedEventSource.close();
    sharedEventSource = null;
  }

  isConnecting = true;
  notifyStateListeners('connecting');
  
  const es = new EventSource('/fe/api/events');
  
  es.onopen = () => {
    reconnectAttempts = 0;
    isConnecting = false;
    notifyStateListeners('connected');
  };
  
  es.onmessage = (event) => {
    try {
      const data: Event = JSON.parse(event.data);
      subscribers.forEach((callback) => callback(data));
    } catch (e) {
      console.error('Failed to parse SSE event:', e);
    }
  };

  es.onerror = () => {
    isConnecting = false;
    
    if (es.readyState === EventSource.CLOSED) {
      notifyStateListeners('disconnected');
      
      // Exponential backoff reconnection
      const delay = Math.min(
        RECONNECT_BASE_DELAY * Math.pow(2, reconnectAttempts),
        RECONNECT_MAX_DELAY
      );
      reconnectAttempts++;
      
      console.warn(`SSE connection closed, reconnecting in ${delay}ms (attempt ${reconnectAttempts})...`);
      
      // Clear any existing reconnect timeout
      if (reconnectTimeout) {
        clearTimeout(reconnectTimeout);
      }
      
      reconnectTimeout = setTimeout(() => {
        // Only reconnect if there are still subscribers
        if (subscribers.size > 0) {
          createEventSource();
        }
      }, delay);
    }
  };
  
  sharedEventSource = es;
}

function getSharedEventSource() {
  if (!sharedEventSource && !isConnecting) {
    createEventSource();
  }
  return sharedEventSource;
}

function subscribe(callback: (data: Event) => void, onStateChange?: (state: ConnectionState) => void) {
  subscribers.add(callback);
  if (onStateChange) {
    stateListeners.add(onStateChange);
    // Notify current state immediately
    if (sharedEventSource?.readyState === EventSource.OPEN) {
      onStateChange('connected');
    } else if (isConnecting) {
      onStateChange('connecting');
    } else {
      onStateChange('disconnected');
    }
  }
  getSharedEventSource();
  
  return () => {
    subscribers.delete(callback);
    if (onStateChange) {
      stateListeners.delete(onStateChange);
    }
    
    // Only close if no more subscribers and no pending reconnects
    if (subscribers.size === 0 && sharedEventSource) {
      // Cancel any pending reconnection
      if (reconnectTimeout) {
        clearTimeout(reconnectTimeout);
        reconnectTimeout = null;
      }
      reconnectAttempts = 0;
      sharedEventSource.close();
      sharedEventSource = null;
      isConnecting = false;
    }
  };
}

export function useEvents(selectedRequestId: string | null, autoScroll: boolean) {
  const [eventsMap, setEventsMap] = useState<Record<string, Event[]>>({});
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const containerRef = useRef<HTMLDivElement | null>(null);
  
  // Track key count with ref to avoid Object.keys() on every event
  const keyCountRef = useRef(0);

  useEffect(() => {
    const handleEvent = (data: Event) => {
      // Most events use 'id', but loop detection events use 'request_id'
      const reqId = data.data?.id || data.data?.request_id;
      if (reqId) {
        setEventsMap((prev) => {
          const isNewKey = !(reqId in prev);
          
          const requestEvents = [...(prev[reqId] || []), data];
          
          // Limit events per request
          const trimmedEvents = requestEvents.length > MAX_EVENTS_PER_REQUEST
            ? requestEvents.slice(-MAX_EVENTS_PER_REQUEST)
            : requestEvents;
          
          const updated = {
            ...prev,
            [reqId]: trimmedEvents,
          };
          
          // Prune old request IDs if map grows too large
          // Only check when we add a new key to avoid Object.keys() on every event
          if (isNewKey) {
            keyCountRef.current++;
            if (keyCountRef.current > MAX_REQUESTS_IN_MAP) {
              // Delete oldest keys - O(k) where k is small
              const toRemove = keyCountRef.current - MAX_REQUESTS_IN_MAP;
              const keys = Object.keys(updated);
              for (let i = 0; i < toRemove; i++) {
                delete updated[keys[i]];
              }
              keyCountRef.current = MAX_REQUESTS_IN_MAP;
            }
          }
          
          return updated;
        });
      }
    };

    return subscribe(handleEvent, setConnectionState);
  }, []);

  // Derive displayedEvents with useMemo instead of state
  const displayedEvents = useMemo(() => {
    if (!selectedRequestId) return [];
    return eventsMap[selectedRequestId] || [];
  }, [selectedRequestId, eventsMap]);

  // Auto-scroll when new events arrive
  useEffect(() => {
    if (autoScroll && containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [displayedEvents, autoScroll]);

  const clearEvents = useCallback(() => {
    if (selectedRequestId) {
      setEventsMap((prev) => {
        const updated = {
          ...prev,
          [selectedRequestId]: [],
        };
        // Sync ref count with actual key count
        keyCountRef.current = Object.keys(updated).length;
        return updated;
      });
    }
  }, [selectedRequestId]);

  return {
    eventsMap,
    displayedEvents,
    containerRef,
    clearEvents,
    connectionState,
  };
}

// Hook to detect events that should trigger request list refresh
// Debounces SSE-driven refetches to avoid cascading HTTP calls when multiple events arrive close together
export function useEventRefresh(onRefresh: () => void) {
  // Single debounce ref for all refetches
  const refreshDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Keep ref to latest onRefresh to avoid stale closure and unnecessary re-subscriptions
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;

  useEffect(() => {
    // Event types that trigger data refetch
    const refreshTypes = [
      'request_started',
      'request_completed',
      'retry_attempt',
      'error_max_upstream_error_retries',
      'timeout_idle',
      'loop_interrupted',
      'fallback_triggered',
      'all_models_failed',
      'auth_failed',
    ];

    const debouncedRefresh = () => {
      if (refreshDebounceRef.current) {
        clearTimeout(refreshDebounceRef.current);
      }
      refreshDebounceRef.current = setTimeout(() => {
        onRefreshRef.current();
      }, 300);
    };

    const handleEvent = (data: Event) => {
      if (refreshTypes.includes(data.type)) {
        debouncedRefresh();
      }
    };

    const unsubscribe = subscribe(handleEvent);

    return () => {
      unsubscribe();
      if (refreshDebounceRef.current) {
        clearTimeout(refreshDebounceRef.current);
      }
    };
  }, []); // No deps - subscribe once, use ref for onRefresh
}
