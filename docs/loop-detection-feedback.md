# Loop Detection Plan Review Feedback

Overall, the loop detection plan is a very well-thought-out, comprehensive, and well-structured approach. By separating detection into specialized heuristics (exact match, similarity, action graph, etc.) rather than relying on another LLM, this perfectly aligns with proxy-level performance requirements.

Here is a breakdown of the plan’s strengths, along with constructive feedback and recommendations to address the "Open Questions" and potential pitfalls.

## 🌟 Strengths

1. **Multi-Faceted Detection Strategy**: Addressing loops from multiple angles (text similarity, repeating actions, internal reasoning repetition) is the right approach. LLMs fail in different ways depending on their system prompts and capabilities.
2. **Performance Awareness**: The plan explicitly outlines bounds (window sizes, MaxCycleLength, target latency < 5ms). The use of ring buffers (sliding windows) and localized hashing (SimHash) will keep CPU and memory overhead low in the proxy layer.
3. **Phased Rollout**: Breaking this down into Core Detection -> Integration -> Advanced Detection -> Recovery is an excellent way to safely deploy without breaking existing proxy traffic.
4. **Rich Interruption Types**: Defining both Soft (warning/visual indication) and Hard (request interruption + model prompt injection) recovery types gives flexibility.

---

## 🛠️ Constructive Feedback & Potential Pitfalls

### 1. Stream Processing Granularity
* **The issue:** The code snippet `for msg := range messageStream { result := detector.Analyze(msg) }` implies running detection on every streamed chunk. Chunks are often 1-5 characters long.
* **Recommendation:** You cannot reliably run SimHash or Trigram repetition on partial sentences. You should buffer the stream into meaningful units—such as sentences, complete tool-call JSON blocks, or `<thought>` blocks. Alternatively, only run the heavy detectors periodically (e.g., every 50 tokens or at the end of a message), while running exact tool-call matching as soon as tool inputs are complete.

### 2. Context Window Contamination During Recovery
* **The issue:** The Hard Interruption strategy suggests injecting a system message like: `"Your previous response was repetitive. Please... "`.
* **Recommendation:** Due to the autoregressive nature of LLMs, if their context window contains 5 repetitions of a mistake, they are highly likely to repeat it *again*, even if you append a warning to the end (in-context reinforcement). If you do a Hard Interruption and return control to the LLM, you should strongly consider **truncating or summarizing the looping history** from the message bus (e.g., replacing the 5 failed loops with a single `[System: The previous 5 attempts looped repeating X. Try a different strategy]`) before passing the context back to the model.

### 3. SimHash Limitations on Small Inputs
* **The issue:** SimHash can yield high false-positive similarity scores for very short pieces of text.
* **Recommendation:** Set a minimum token length to apply SimHash (e.g., `if tokenCount < 15 { return strategy.Exact }`). Short messages like "I will check the file." or "Working on it..." will trigger false loops if not bounded by length.

### 4. Handling "Deep Thinking" models (o1, o3-mini)
* **The issue:** Reasoning models generate very long, iterative text that naturally loops backward and forward while evaluating approaches.
* **Recommendation:** For reasoning/thinking models, the `TrigramThreshold` should be significantly more forgiving. Ensure the `ThinkingAnalyzer` only targets `<think>` nodes or the designated reasoning stream, preventing it from mixing with action schemas.

---

## 💡 Answers to "Open Questions"

1. **Threshold tuning**: Start in "Shadow Mode". Implement Phase 1 & 2 without any Hard Interruption. Just log when a loop *would* have been detected. Use these logs to tune your defaults. `SimilarityThreshold: 0.85` is a good starting point for text, but you might find `0.90` is safer for code-heavy output where syntax naturally repeats.
2. **Streaming vs batch**: **Batch** is required for text heuristics. You should accumulate stream chunks in a buffer and run the detector only when the buffer yields a complete sentence/thought or when a specific token limit (e.g. 20 tokens) is reached.
3. **Multi-turn context**: **Yes**. Action Pattern Detection and Progress Stagnation are largely useless within a single streaming response (unless the model supports parallel tool-calling in one stream). To catch an agent stuck in a "Read file -> Fail -> Read file -> Fail" loop, the `MessageContext` window *must* persist across the multi-turn lifecycle of a single user request.
4. **Model-specific patterns**: **Yes, eventually.** However, keep it simple for V1. Start with global configurations. In V2, you could allow overriding `Config` based on regex matching `req.Model`.
5. **False positive handling**: Be extremely conservative. A false positive loop detection that breaks the user's workflow is far more frustrating than waiting an extra 5 seconds for a model to loop out. Always favor "Soft Interruption/Warning" until you are highly confident in your thresholds.

## Conclusion
The architecture is solid and the algorithmic choices are highly appropriate for a fast Go-based proxy. The primary risks are related to running block-level algorithms (Hashing/N-grams) on fractured streams, and not scrubbing the LLM's context window after a loop occurs. Moving forward with Phase 1 sounds great!
