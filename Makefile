# Thin wrapper over Taskfile.yml for those who reach for make. Install Task
# (https://taskez.dev) and run `task --list` to see everything.

.DEFAULT_GOAL := help
TASK := $(shell command -v task 2>/dev/null)

.PHONY: help setup generate fmt lint vuln test test-integration build run dev migrate up down ci

help:
	@task --list 2>/dev/null || echo "Install Task: https://taskfile.dev"

setup generate fmt lint vuln test build run dev migrate up down ci:
	@task $@

test-integration:
	@task test:integration
