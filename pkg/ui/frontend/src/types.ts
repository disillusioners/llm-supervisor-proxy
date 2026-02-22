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
}

export interface RequestDetail extends Request {
  messages: Message[];
  response?: string;
}

export type EventType =
  | 'request_started'
  | 'request_completed'
  | 'retry_attempt'
  | 'error_max_upstream_error_retries'
  | 'timeout_idle'
  | 'error';

export interface EventData {
  id?: string;
  timeout?: string;
  attempt?: number;
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
