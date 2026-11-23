.PHONY: build test demo local web up dataset

build:
	go build -o bin/evalbench ./cmd/evalbench

test:
	go test -race ./...

# The whole pipeline on the mock models: no keys, no network, a few seconds.
demo: build
	rm -f data/mock.db*
	./bin/evalbench import datasets/evalbench-v1.jsonl -config bench/mock.yaml
	./bin/evalbench run -config bench/mock.yaml -dataset evalbench-v1 -models mock-strong,mock-weak -name "Mock demo"

# The local-model benchmark from the README (needs Ollama and the three models).
local: build
	./bin/evalbench import datasets/evalbench-v1.jsonl -config bench/local.yaml
	./bin/evalbench run -config bench/local.yaml -dataset evalbench-v1 -models qwen3-4b,llama3.2-3b,gemma3-1b

# Rebuild the bundled dataset; its asserts check every computed answer.
dataset:
	python3 datasets/make_evalbench_v1.py

web:
	cd web && npm ci && npm run build

up:
	docker compose up --build
