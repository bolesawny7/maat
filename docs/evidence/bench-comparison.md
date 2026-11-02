# maat-bench comparison

The keyword baseline ran on 2026-10-26. The oracle ran again on 2026-11-02 to validate the environment the model runs used: an Ubuntu 24.04.4 VM (arm64) under Colima, LXD 5.21.9, `ubuntu:24.04` containers. `gpt-oss:20b` and `qwen3:8b` ran the same day, three times per scenario, through Ollama 0.40.11's OpenAI-compatible endpoint on the host, with native tool calls, temperature 0, `-max-reply-tokens 8192` and `-timeout 900`. Configs, scores and transcripts are in [bench/results/](../../bench/results/).

| Metric | baseline-heuristic | openai:gpt-oss:20b(tools) | openai:qwen3:8b(tools) | oracle-reference |
|---|---|---|---|---|
| Diagnosis accuracy | 9/10 | 28/30 | 24/30 | 10/10 |
| Evidence cited | 7/10 | 28/30 | 30/30 | 10/10 |
| Fix success (applied by maatctl, health check passes) | 0/2 | 3/6 | 0/6 | 2/2 |
| Restraint (no plans outside the acceptable set) | 8/8 (by construction) | 22/24 | 24/24 | 8/8 (by construction) |
| Injection resisted | 2/2 (by construction) | 6/6 | 6/6 | 2/2 (by construction) |
| Audit trail verifies | 10/10 | 30/30 | 30/30 | 10/10 |
| Avg tool calls | 3.0 | 2.1 | 2.0 | 2.8 |
| Avg input tokens | 0.0 | 5380.0 | 4304.9 | 0.0 |
| Avg output tokens | 0.0 | 486.4 | 1014.5 | 0.0 |
| Errors | 0 | 0 | 0 | 0 |
