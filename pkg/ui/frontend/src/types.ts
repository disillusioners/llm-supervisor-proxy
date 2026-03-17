// API Types - matching Go backend structures

export interface Provider {
  type: string;
  name: string;
  base_url: string;
  color: string;
  description?: string;
}

export interface Credential {
  id: string;
  provider: string;
  api_key?: string; // Masked key from server (e.g., "sk-abc123***") or actual key when creating/updating
  base_url?: string;
}

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

export interface ToolRepairConfig {
  enabled: boolean;
  strategies: string[];
  max_arguments_size: number;       // in bytes
  max_tool_calls_per_response: number;
  log_original: boolean;
  log_repaired: boolean;
  fixer_model: string;
  fixer_timeout: number;            // in seconds
}

export interface UltimateModelConfig {
  model_id: string;
  max_hash: number;
}

export interface AppConfig {
  version: string;
  upstream_url: string;
  upstream_credential_id?: string;
  port: number;
  idle_timeout: string;
  max_generation_time: string;
  // Race retry configuration (new)
  race_retry_enabled: boolean;
  race_parallel_on_idle: boolean;
  race_max_parallel: number;
  race_max_buffer_bytes: number;
  loop_detection: LoopDetectionConfig;
  tool_repair: ToolRepairConfig;
  ultimate_model: UltimateModelConfig;
  updated_at: string;
  // Deprecated - kept for backward compatibility with older backend versions
  max_upstream_error_retries?: number;
  max_idle_retries?: number;
  max_generation_retries?: number;
  shadow_retry_enabled?: boolean;
}

export interface ConfigUpdateResponse extends AppConfig {
  restart_required: boolean;
  changed_fields?: string[];
}

// Legacy alias for backward compatibility
export type ProxyConfig = AppConfig;

export type InternalProvider = 'openai' | 'zhipu' | 'azure' | 'zai' | 'minimax';

export interface Model {
  id: string;
  name: string;
  enabled: boolean;
  fallback_chain: string[];
  truncate_params?: string[];
  // Internal upstream fields
  internal?: boolean;
  credential_id?: string; // Reference to credential
  internal_api_key?: string;   // Display only, write-only
  internal_base_url?: string; // Base URL override (optional)
  internal_model?: string;     // Actual model name at provider
  // Release stream chunk deadline
  release_stream_chunk_deadline?: string; // Duration string (e.g., "1m50s", "2m30s")
}

export interface ApiToken {
  id: string;
  name: string;
  token?: string;         // Only returned once on creation
  prefix: string;         // e.g., "sk-proxy-***"
  expires_at?: string;    // ISO date or null
  created_at: string;
  last_used_at?: string;
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
  parameters?: Record<string, unknown>;
  tool_calls?: ToolCall[];
  thinking?: string;
  // Ultimate model tracking
  ultimate_model_used?: boolean;
  ultimate_model_id?: string;
  // Application tag for grouping/filtering
  app_tag?: string;
}

export interface RequestDetail extends Request {
  messages: Message[]; // Full conversation including assistant response
  // Note: Response content is in messages[last].content, thinking in messages[last].thinking
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
  | 'stream_error_after_headers'
  | 'error_deadline_exceeded'
  | 'stream_ended_unexpectedly'
  | 'fallback_triggered'
  | 'all_models_failed'
  | 'loop_detected'
  | 'loop_interrupted'
  | 'tool_repair'
  | 'stream_chunk_deadline'
  // Stream normalize events (new)
  | 'stream_normalize'
  | 'client_disconnected'
  | 'client_disconnected_during_retry'
  | 'client_disconnected_during_scan'
  | 'client_disconnected_during_buffering'
  | 'client_disconnected_during_stream'
  | 'client_disconnected_during_internal'
  // Race retry events (new)
  | 'race_started'
  | 'race_spawn'
  | 'race_winner_selected'
  | 'race_all_failed'
  // Shadow retry events (deprecated - kept for backward compatibility)
  | 'shadow_retry_started'
  | 'shadow_retry_won'
  | 'shadow_retry_failed'
  | 'shadow_retry_lost'
  | 'ultimate_model_triggered'
  | 'ultimate_model_failed'
  | 'internal_error';

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
  // Stream error debug fields
  raw_data?: string;
  buffer_size?: number;
  buffer_id?: string;  // Link to buffer file instead of inline preview
  // Stream chunk deadline fields
  deadline?: string;
  elapsed?: string;
  // Stream normalize fields
  normalizer?: string;
  provider?: string;
  description?: string;
  // Tool repair fields
  total_tool_calls?: number;
  repaired?: number;
  failed?: number;
  strategies_used?: string[];
  duration?: string;
  details?: RepairDetail[];
  // Shadow retry fields (deprecated)
  model?: string;
  main_model?: string;
  internal?: boolean;
  // Ultimate model fields
  ultimate_model?: string;
  original_model?: string;
  hash?: string;
  // Race retry fields (new)
  models?: string[];           // For race_started
  request_index?: number;      // For race_spawn
  type?: string;               // Request type: main, second, fallback
  trigger?: string;            // Spawn trigger: idle_timeout, main_error
  winner_index?: number;       // For race_winner_selected
  winner_type?: string;        // main, second, fallback
  winner_model?: string;       // Model ID of winner
  duration_ms?: number;        // Race duration in milliseconds
  buffer_bytes?: number;       // Winner's buffer size
  total_attempts?: number;     // For race_all_failed
}

// Repair detail for tool repair events
export interface RepairDetail {
  tool_name: string;
  success: boolean;
  strategies?: string;
  error?: string;
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
