.PHONY: all test simulate demo clean lint fmt

# 默认目标
all: test

# 运行所有测试
test:
	go test ./... -v -count=1

# 端到端全场景模拟
simulate:
	go run ./cmd/simulate/

# 运行指定模块的 demo（用法：make demo MOD=01-rpc-client）
demo:
	@if [ -z "$(MOD)" ]; then echo "Usage: make demo MOD=01-rpc-client"; exit 1; fi
	go run ./$(MOD)/demo/

# 代码格式化
fmt:
	gofmt -w .

# 代码检查
lint:
	go vet ./...

# 清理
clean:
	go clean -cache -testcache
