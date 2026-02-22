package database

// defaultModelsJSON contains the default models configuration
// to seed the database on first run.
const defaultModelsJSON = `{
  "models": [
    {
      "id": "glm-5",
      "name": "GLM-5",
      "enabled": true,
      "fallback_chain": ["MiniMax-M2.5"],
      "truncate_params": ["max_completion_tokens", "store"]
    },
    {
      "id": "MiniMax-M2.5",
      "name": "MiniMax M2.5",
      "enabled": true,
      "fallback_chain": ["glm-5"]
    }
  ]
}`

// defaultConfigJSON contains the default proxy configuration
// to seed the database on first run.
const defaultConfigJSON = `{
  "version": "1.0",
  "upstream_url": "http://litellm-service.litellm.svc.cluster.local:4000",
  "port": 4321,
  "idle_timeout": "90s",
  "max_generation_time": "300s",
  "max_upstream_error_retries": 1,
  "max_idle_retries": 2,
  "max_generation_retries": 1,
  "loop_detection": {
    "enabled": true,
    "shadow_mode": true,
    "message_window": 10,
    "action_window": 15,
    "exact_match_count": 3,
    "similarity_threshold": 0.85,
    "min_tokens_for_simhash": 15,
    "action_repeat_count": 3,
    "oscillation_count": 4,
    "min_tokens_for_analysis": 20,
    "thinking_min_tokens": 100,
    "trigram_threshold": 0.3,
    "max_cycle_length": 5,
    "reasoning_model_patterns": ["o1", "o3", "deepseek-r1"],
    "reasoning_trigram_threshold": 0.15
  },
  "updated_at": "2024-01-01T00:00:00Z"
}`
