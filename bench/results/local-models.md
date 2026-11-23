# Three local models on an M1, judged by qwen3-4b

Dataset **evalbench-v1** (72 items) · run `run_20261123_071227_13f5` · status done · took 1h32m4s

Judge: **qwen3-4b**. Items with an objective answer are scored by code; open-ended answers and hallucination probes by the judge.

## Results

| Model | Accuracy (95% CI) | Judge score | Hallucination rate | p50 latency | p95 latency | Output tok/s | Cost | Cost per correct | Errors |
|---|---|---|---|---|---|---|---|---|---|
| qwen3-4b | 94% (87%-98%), 68/72 | 4.78 | 0% (0/13) | 5.6 s | 58.4 s | 6 | local | local | 0 |
| llama3.2-3b | 85% (75%-91%), 61/72 | 4.46 | 23% (3/13) | 3.0 s | 31.8 s | 10 | local | local | 0 |
| gemma3-1b | 68% (57%-78%), 49/72 | 3.81 | 69% (9/13) | 1.5 s | 39.2 s | 19 | local | local | 0 |

## Accuracy by category

| Category | Items | qwen3-4b | llama3.2-3b | gemma3-1b |
|---|---|---|---|---|
| arithmetic | 10 | 100% (10/10) | 90% (9/10) | 80% (8/10) |
| reasoning | 10 | 90% (9/10) | 70% (7/10) | 70% (7/10) |
| knowledge | 8 | 100% (8/8) | 100% (8/8) | 100% (8/8) |
| instructions | 10 | 90% (9/10) | 90% (9/10) | 70% (7/10) |
| extraction | 8 | 100% (8/8) | 88% (7/8) | 75% (6/8) |
| grounded-qa | 10 | 100% (10/10) | 100% (10/10) | 90% (9/10) |
| hallucination-probe | 10 | 90% (9/10) | 70% (7/10) | 0% (0/10) |
| writing | 6 | 83% (5/6) | 67% (4/6) | 67% (4/6) |

## How far to trust the judge

On items with an objective answer, the code grader's verdict is taken as ground truth and compared with the judge's.

| Judge | Agrees with code grader | Passed wrong answers | Failed right answers | Cohen's kappa | Consistency across samples | Unreadable verdicts | Cost |
|---|---|---|---|---|---|---|---|
| qwen3-4b (primary) | 93% (142/153) | 5 | 6 | 0.68 | - | 0 | local |
| llama3.2-3b | 93% (143/153) | 8 | 2 | 0.65 | - | 0 | local |

**qwen3-4b, agreement with the code grader by model answered:**

| Answers from | Agreement | Passed wrong | Failed right |
|---|---|---|---|
| gemma3-1b | 90% (46/51) | 2 | 3 |
| llama3.2-3b | 94% (48/51) | 2 | 1 |
| qwen3-4b (itself) | 94% (48/51) | 1 | 2 |

**llama3.2-3b, agreement with the code grader by model answered:**

| Answers from | Agreement | Passed wrong | Failed right |
|---|---|---|---|
| gemma3-1b | 84% (43/51) | 7 | 1 |
| llama3.2-3b (itself) | 96% (49/51) | 1 | 1 |
| qwen3-4b | 100% (51/51) | 0 | 0 |

**Judges compared with each other (all answers):**

| Judges | Same verdict | Cohen's kappa |
|---|---|---|
| qwen3-4b vs llama3.2-3b | 87% (188/216) | 0.51 |

## Flagged for review

- `hallucination`: 15
- `heuristic_disagrees`: 5
- `judge_disagrees`: 11
- `truncated`: 3

## Models

| Name | Provider | Model | Settings |
|---|---|---|---|
| qwen3-4b | ollama | `qwen3:4b-instruct` | max_tokens 1024, temperature 0, local |
| llama3.2-3b | ollama | `llama3.2:3b` | max_tokens 1024, temperature 0, local |
| gemma3-1b | ollama | `gemma3:1b` | max_tokens 1024, temperature 0, local |
