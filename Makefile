SHELL := /usr/bin/env bash

.PHONY: format format-check lint test typecheck security ci

format:
	go fmt ./...

format-check:
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "gofmt check failed; run 'make format'"; \
		gofmt -l .; \
		exit 1; \
	fi

lint:
	go vet ./...

test:
	go test ./...

typecheck:
	go test -run '^$$' ./...

security:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	go run ./cmd/secretscan

ci: format-check lint typecheck test security
