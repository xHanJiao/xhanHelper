.PHONY: dev test check-env clean help

# 开发服务
dev: check-env
	@echo "=== 启动 xhanHelper Python 开发服务器 (SQLite) ==="
	. venv/bin/activate && uvicorn app.main:app --port 8080 --reload

# 环境变量检查
check-env:
	@ok=true; \
	if [ "$$APP_ENV" = "prod" ] && [ -z "$$LLM_API_KEY" ]; then \
		echo "❌ LLM_API_KEY 未设置"; ok=false; \
	fi; \
	if [ "$$APP_ENV" = "prod" ] && [ -z "$$XHAN_HELPER_DB_PASSWORD" ]; then \
		echo "❌ XHAN_HELPER_DB_PASSWORD 未设置"; ok=false; \
	fi; \
	if [ "$$APP_ENV" = "prod" ] && [ -z "$$REGISTRATION_SECRET" ]; then \
		echo "❌ REGISTRATION_SECRET 未设置"; ok=false; \
	fi; \
	if ! $$ok; then \
		echo ""; \
		echo "dev 环境无需环境变量（使用 dev-secret），prod 环境需以上变量"; \
		exit 1; \
	fi; \
	if [ -z "$$APP_ENV" ]; then \
		echo "💡 APP_ENV 未设置，默认使用 dev (SQLite + dev-secret)"; \
	fi; \
	echo "✅ 环境变量检查通过"

# API 测试
test: check-env
	@echo "=== 清理旧数据库 ==="
	@rm -f data/xhan_helper.db
	@echo "=== 启动测试服务器 ==="
	@. venv/bin/activate && uvicorn app.main:app --port 8080 --log-level warning &
	@sleep 3
	@echo "=== 运行 API 测试 ==="
	@bash scripts/api-test.sh; EXIT=$$?
	@echo ""
	@echo "=== 停止测试服务器 ==="
	@kill $$(lsof -ti:8080) 2>/dev/null || true
	@exit $$EXIT

# 清理
clean:
	rm -f data/xhan_helper.db
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true

help:
	@echo "  make dev     - 启动开发服务器"
	@echo "  make test    - API 自动化测试"
	@echo "  make clean   - 清理数据库和缓存"
