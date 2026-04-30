.PHONY: dev check-env build clean test

# 启动开发服务器
dev: check-env
	@echo "=== 启动 xhanHelper 开发服务器 (SQLite) ==="
	go run ./cmd/xhan-helper/

# 检查必要的环境变量
check-env:
	@ok=true; \
	if [ -z "$$LLM_API_KEY" ]; then \
		echo "❌ LLM_API_KEY 未设置"; \
		echo "   export LLM_API_KEY=\"your-doubao-api-key\""; \
		ok=false; \
	fi; \
	if [ "$$APP_ENV" = "prod" ] && [ -z "$$DB_PASSWORD" ]; then \
		echo "❌ DB_PASSWORD 未设置 (prod 环境需要)"; \
		echo "   export DB_PASSWORD=\"your-mysql-password\""; \
		ok=false; \
	fi; \
	if ! $$ok; then \
		echo ""; \
		echo "dev 环境只需 LLM_API_KEY，prod 环境还需 DB_PASSWORD"; \
		exit 1; \
	fi; \
	if [ -z "$$APP_ENV" ]; then \
		echo "💡 APP_ENV 未设置，默认使用 dev (SQLite)"; \
	fi; \
	echo "✅ 环境变量检查通过"

# API 自动化测试
test: check-env
	@echo "=== 编译 ==="
	@go build -o /tmp/xhan-test-server ./cmd/xhan-helper/ || exit 1
	@echo "=== 清理旧数据库 ==="
	@rm -f data/xhan_helper.db
	@echo "=== 启动测试服务器 ==="
	@/tmp/xhan-test-server &
	@sleep 2
	@echo "=== 运行 API 测试 ==="
	@bash scripts/api-test.sh; EXIT=$$?
	@echo ""
	@echo "=== 停止测试服务器 ==="
	@kill $$(lsof -ti:8080) 2>/dev/null || true
	@rm -f /tmp/xhan-test-server
	@exit $$EXIT

# 编译生产二进制
build:
	go build -o xhan-helper ./cmd/xhan-helper/

# 清理
clean:
	rm -f xhan-helper
	rm -f data/xhan_helper.db
