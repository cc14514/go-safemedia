SHELL := /bin/bash
BIN   := bin/safemedia
PKG   := ./...

.PHONY: all build test test-l1 test-l2 test-l3 lint vet fmt clean smoke

all: build

build:
	go build -o $(BIN) ./cmd/safemedia

## L1 单元测试（开发者自检门禁）
test-l1:
	go test ./internal/... ./cmd/...

## L2 集成测试（Feature 完成门禁）
test-l2:
	go test ./tests/integration/...

## L3 E2E（QA 交付门禁，需真实浏览器与样本视频）
##   SAFEMEDIA_E2E_SAMPLES=<dir>  指定真实样本目录（见 tests/e2e/fetch-samples.sh）
##   SAFEMEDIA_E2E_NO_BROWSER=1   跳过无头浏览器解码校验
test-l3:
	go test ./tests/e2e/... -v
	@echo "▶ L3 报告已生成：tests/e2e/REPORT.md"

test: test-l1 test-l2

vet:
	go vet $(PKG)

fmt:
	gofmt -l -w .

lint: vet

smoke: build
	./$(BIN) --help

clean:
	rm -rf bin