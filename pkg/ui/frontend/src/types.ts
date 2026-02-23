// API Types - matching Go backend structures

export interface LoopDetectionConfig {
  enabled: boolean;
  shadow_mode: boolean;
  message_window: number;
  action_window: number;
  exact_match_count: number;
  similarity_threshold: number;
  min_tokens_for_simhash: number;
  action_repeat_count: number;
  oscillation_count: number;
  min_tokens_for_analysis: number;
  // Phase 3
  thinking_min_tokens: number;
  trigram_threshold: number;
  max_cycle_length: number;
  reasoning_model_patterns: string[];
  reasoning_trigram_threshold: number;
}

export interface AppConfig {
  version: string;
  upstream_url: string;
  port: number;
  idle_timeout: string;
  max_generation_time: string;
  max_upstream_error_retries: number;
  max_idle_retries: number;
  max_generation_retries: number;
  loop_detection: LoopDetectionConfig;
  updated_at: string;
}

export interface ConfigUpdateResponse extends AppConfig {
  restart_required: boolean;
  changed_fields?: string[];
}

// Legacy alias for backward compatibility
export type ProxyConfig = AppConfig;

export interface Model {
  id: string;
  name: string;
  enabled: boolean;
  fallback_chain: string[];
  truncate_params?: string[]; // Parameters to strip from the request before forwarding (e.g. ["max_completion_tokens", "store"])
}

export interface Message {
  role: 'user' | 'assistant' | 'system';
  content: string;
  thinking?: string;
  tool_calls?: ToolCall[];
}

export interface ToolCall {
  id: string;
  type: 'function';
  function: {
    name: string;
    arguments: string;
  };
}

export interface Request {
  id: string;
  model: string;
  status: 'running' | 'completed' | 'failed' | 'retrying';
  startTime: string;
  duration: string;
  retries: number;
  error?: string;
  // Original request metadata
  original_model?: string;
  is_stream?: boolean;
  fallback_used?: string[];
}

export interface RequestDetail extends Request {
  messages: Message[];
  response?: string;
  thinking?: string;
  parameters?: Record<string, unknown>;
}

export type EventType =
  | 'request_started'
  | 'request_completed'
  | 'retry_attempt'
  | 'error_max_upstream_error_retries'
  | 'timeout_idle'
  | 'error'
  | 'upstream_error'
  | 'upstream_error_status'
  | 'upstream_error_status_retry'
  | 'stream_error'
  | 'stream_error_chunk'
  | 'error_deadline_exceeded'
  | 'stream_ended_unexpectedly'
  | 'fallback_triggered'
  | 'all_models_failed'
  | 'loop_detected'
  | 'loop_interrupted'
  | 'client_disconnected_during_retry';

export interface EventData {
  id?: string;
  timeout?: string;
  attempt?: number;
  error?: string;
  status?: number;
  // Fallback fields
  from_model?: string;
  to_model?: string;
  reason?: string;
  // Loop detection fields
  request_id?: string;
  strategy?: string;
  severity?: string;
  evidence?: string;
  confidence?: number;
  pattern?: string[];
  repeat_count?: number;
  shadow_mode?: boolean;
}

export interface Event {
  type: EventType;
  timestamp: number;
  data: EventData | null;
}

// UI State Types
export interface AppState {
  selectedRequestId: string | null;
  autoScroll: boolean;
  showConfig: boolean;
}
