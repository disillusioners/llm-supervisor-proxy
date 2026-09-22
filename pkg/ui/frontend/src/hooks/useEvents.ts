import { useState, useEffect, useCallback, useRef, useMemo } from 'preact/hooks';
import type { Event, EventType } from '../types';

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

// Hook to detect events that should trigger request list refresh.
//
// Strategy:
//   * 3 s trailing debounce so SSE bursts (continuous agent traffic
//     emits request_started/completed rapidly) coalesce into one
//     refetch. The previous 300 ms value matched the cadence of agent
//     traffic 1:1 → one refetch every few seconds → constant 96 MB
//     transfers. 3 s is the lower bound of the user task window so it
//     does not feel stale while cutting network load by ~10×.
//   * When an SSE payload carries enough data to update a list row
//     directly (`patchListEntry`), apply the patch immediately so the
//     user sees instant visual feedback, AND kick off the debounced
//     full-refetch so the row gets reconciled with backend ground
//     truth. The patch is purely a UI optimisation; the refresh is
//     the source of truth.
//   * Dead event names (retry_attempt, timeout_idle, loop_interrupted)
//     were removed: the backend never publishes them, so they were
//     unreachable noise that made the hook longer than necessary.
//
// Callers that don't need patch-by-id can omit `options`; behaviour
// degrades gracefully to plain debounced full-refetch.
export function useEventRefresh(
  onRefresh: () => void,
  options?: {
    patchListEntry?: <K extends keyof import('../types').RequestListItem>(
      id: string,
      partial: Pick<import('../types').RequestListItem, K>,
    ) => void;
    fetchAndPatchById?: (id: string) => Promise<void>;
  },
) {
  // Single debounce ref for all refetches
  const refreshDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Keep ref to latest callbacks to avoid stale closure and unnecessary re-subscriptions
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;
  const patchRef = useRef(options?.patchListEntry);
  patchRef.current = options?.patchListEntry;
  const fetchByIdRef = useRef(options?.fetchAndPatchById);
  fetchByIdRef.current = options?.fetchAndPatchById;

  useEffect(() => {
    // Event types that benefit from patch-by-id (status transitions on an
    // existing request). Other event types fall through to the full
    // refetch debounce below.
    const PATCH_TYPES = new Set<EventType>(['request_completed']);

    // Event types that trigger data refetch (full list refresh).
    // Anything else is intentionally ignored — the backend doesn't
    // publish retry_attempt / timeout_idle / loop_interrupted, so the
    // original list was dead code.
    const REFRESH_TYPES: EventType[] = [
      'request_started',
      'fallback_triggered',
      'all_models_failed',
      'auth_failed',
      'error_max_upstream_error_retries',
    ];

    const debouncedRefresh = () => {
      if (refreshDebounceRef.current) {
        clearTimeout(refreshDebounceRef.current);
      }
      refreshDebounceRef.current = setTimeout(() => {
        onRefreshRef.current();
      }, 3000);
    };

    const handleEvent = (data: Event) => {
      // Try to patch a single list entry first; this is the optimistic
      // UI fast-path for status transitions on an existing row. The
      // trailing debouncedRefresh() below provides the ground-truth
      // reconciliation.
      if (PATCH_TYPES.has(data.type) && patchRef.current) {
        const id = data.data?.id || data.data?.request_id;
        if (typeof id === 'string') {
          if (data.type === 'request_completed') {
            // request_completed carries enough info to flip the row to
            // 'completed' client-side. If the row is NOT in the list yet,
            // patchListEntry silently no-ops; the trailing refresh will
            // pick it up. fetchAndPatchById is only invoked on
            // request_started below — it fetches and inserts new rows.
            patchRef.current(id, { status: 'completed' });
            debouncedRefresh();
            return;
          }
        }
      }

      // request_started is the event where we may need to insert a
      // brand-new row. fetchAndPatchById is a no-op if the row already
      // exists, so duplicate events for known IDs are safe.
      if (data.type === 'request_started' && fetchByIdRef.current) {
        const id = data.data?.id || data.data?.request_id;
        if (typeof id === 'string') {
          fetchByIdRef.current(id);
          // Still debounce-refresh so we pick up rows we missed.
          debouncedRefresh();
          return;
        }
      }

      if (REFRESH_TYPES.includes(data.type)) {
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
  }, []); // No deps - subscribe once, use refs for callbacks
}
