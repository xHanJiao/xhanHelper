.PHONY: dev dev-full check-env build clean test build-linux build-frontend deploy deploy-backend deploy-frontend deploy-only help logs

# 服务器配置
SERVER_USER=${DEPLOY_USER}
SERVER_HOST=${DEPLOY_HOST}
SERVER_PATH=/srv/xhan-helper
BINARY=xhan-helper
OUTPUT_DIR=dist
FRONTEND_DIR=../front-end

# 默认：显示帮助
help:
	@echo "可用的 make 命令："
	@echo "  make dev            - 本地开发模式启动（dev环境，使用SQLite）"
	@echo "  make dev-full       - 同时启动前后端开发服务器"
	@echo "  make build          - 本地编译（当前系统）"
	@echo "  make build-linux    - 交叉编译 Linux amd64 版本（用于阿里云服务器）"
	@echo "  make build-frontend - 构建前端静态文件"
	@echo "  make deploy         - 一键部署：编译前后端 + 上传 + 重启"
	@echo "  make deploy-backend - 仅部署后端（编译 + 上传 + 重启）"
	@echo "  make deploy-frontend- 仅部署前端（构建 + 上传）"
	@echo "  make deploy-only    - 仅上传已编译的二进制和conf（不重新编译）"
	@echo "  make test           - API 自动化测试 (34项)"
	@echo "  make logs           - 实时查看远程服务器日志 (tail -f)"
	@echo "  make clean          - 清理编译产物"

# 启动开发服务器
dev: check-env
	@echo "=== 启动 xhanHelper 开发服务器 (SQLite) ==="
	@mkdir -p logs
	@go run ./cmd/xhan-helper/ 2>&1 | tee logs/dev-$$(date +%Y-%m-%d).log

# 同时启动前后端开发服务器
dev-full: check-env
	@echo "=== 启动前后端开发环境 ==="
	@mkdir -p logs
	@LOG="logs/dev-$$(date +%Y-%m-%d).log"; \
	echo "  日志文件: $$LOG"; \
	go run ./cmd/xhan-helper/ >> "$$LOG" 2>&1 & \
	BPID=$$!; \
	(cd $(FRONTEND_DIR) && npm run dev) & \
	FPID=$$!; \
	sleep 2; \
	echo "  后端: http://localhost:8080"; \
	echo "  前端: http://localhost:5173"; \
	echo "  Ctrl+C 停止所有服务"; \
	trap "kill $$BPID $$FPID 2>/dev/null; true" INT TERM; \
	tail -f "$$LOG"

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
	if [ -z "$$EMBEDDING_API_KEY" ]; then \
		echo "💡 EMBEDDING_API_KEY 未设置，向量检索功能不可用"; \
		echo "   export EMBEDDING_API_KEY=\"your-ark-api-key\""; \
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
	@echo "=== 备份数据库 ==="
	@mkdir -p data
	@if [ -f data/xhan_helper.db ]; then \
		mv data/xhan_helper.db data/xhan_helper.db.backup; \
		echo "   ✓ 已备份: data/xhan_helper.db.backup"; \
	fi
	@echo "=== 启动测试服务器 ==="
	@/tmp/xhan-test-server &
	@sleep 2
	@echo "=== 运行 API 测试 ==="
	@bash scripts/api-test.sh; EXIT=$$?
	@echo ""
	@echo "=== 停止测试服务器 ==="
	@kill $$(lsof -ti:8080) 2>/dev/null || true
	@rm -f /tmp/xhan-test-server
	@echo "=== 恢复数据库 ==="
	@if [ -f data/xhan_helper.db.backup ]; then \
		mv data/xhan_helper.db.backup data/xhan_helper.db; \
		echo "   ✓ 已恢复"; \
	else \
		rm -f data/xhan_helper.db; \
		echo "   ✓ 已清理测试数据库"; \
	fi
	@exit $$EXIT

# 编译生产二进制
build:
	go build -o xhan-helper ./cmd/xhan-helper/

# 交叉编译 Linux amd64
build-linux:
	@echo "=== 交叉编译 Linux amd64 ==="
	@mkdir -p $(OUTPUT_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/$(BINARY) ./cmd/xhan-helper/
	@echo "编译完成: $(OUTPUT_DIR)/$(BINARY)"

# 构建前端
build-frontend:
	@echo "=== 构建前端 ==="
	@cd $(FRONTEND_DIR) && npm run build
	@echo "构建完成: $(FRONTEND_DIR)/dist/"

# 一键部署：前端 + 后端
deploy: build-frontend build-linux
	@if [ -f .env ]; then . ./.env; fi; \
	if [ -z "$$DEPLOY_USER" ] || [ -z "$$DEPLOY_HOST" ]; then \
		echo "❌ 请先设置 .env 文件: DEPLOY_USER=xxx DEPLOY_HOST=xxx"; \
		echo "   参考 .env.example"; \
		exit 1; \
	fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); B=$(OUTPUT_DIR)/$(BINARY); F=$(FRONTEND_DIR)/dist/; \
	echo ""; \
	echo "========================================"; \
	echo "       部署到阿里云服务器 (全量)"; \
	echo "========================================"; \
	echo "1/5 上传前端..."; \
	scp -r $$F $$U@$$H:$$P/front-end/; \
	echo "2/5 上传二进制..."; \
	scp $$B $$U@$$H:$$P/$(BINARY).tmp; \
	echo "3/5 上传配置文件 + 控制脚本..."; \
	scp conf/config.yaml conf/config.prod.yaml $$U@$$H:$$P/conf/; \
	scp scripts/control-xhan-helper.sh $$U@$$H:$$P/; \
	echo "4/5 上传 SQL 迁移文件..."; \
	scp -r migrations/ $$U@$$H:$$P/; \
	echo "5/5 替换二进制并重启..."; \
	ssh $$U@$$H "cd $$P && mv $(BINARY).tmp $(BINARY) && chmod +x $(BINARY) control-xhan-helper.sh && bash control-xhan-helper.sh" || ( \
		echo ""; \
		echo "❌ 启动失败，日志如下："; \
		echo "========================================"; \
		ssh $$U@$$H "tail -50 $$P/logs/xhan-helper-\$$(date +%Y-%m-%d).log"; \
		echo "========================================"; \
		exit 1); \
	echo ""; \
	echo "✅ 部署完成！"

# 仅部署后端
deploy-backend: build-linux
	@if [ -f .env ]; then . ./.env; fi; \
	if [ -z "$$DEPLOY_USER" ] || [ -z "$$DEPLOY_HOST" ]; then \
		echo "❌ 请先设置 .env 文件: DEPLOY_USER=xxx DEPLOY_HOST=xxx"; \
		echo "   参考 .env.example"; \
		exit 1; \
	fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); B=$(OUTPUT_DIR)/$(BINARY); \
	echo ""; \
	echo "========================================"; \
	echo "       部署后端到阿里云服务器"; \
	echo "========================================"; \
	echo "1/4 上传二进制..."; \
	scp $$B $$U@$$H:$$P/$(BINARY).tmp; \
	echo "2/4 上传配置文件 + 控制脚本..."; \
	scp conf/config.yaml conf/config.prod.yaml $$U@$$H:$$P/conf/; \
	scp scripts/control-xhan-helper.sh $$U@$$H:$$P/; \
	echo "3/4 上传 SQL 迁移文件..."; \
	scp -r migrations/ $$U@$$H:$$P/; \
	echo "4/4 替换二进制并重启..."; \
	ssh $$U@$$H "cd $$P && mv $(BINARY).tmp $(BINARY) && chmod +x $(BINARY) control-xhan-helper.sh && bash control-xhan-helper.sh" || ( \
		echo ""; \
		echo "❌ 启动失败，日志如下："; \
		echo "========================================"; \
		ssh $$U@$$H "tail -50 $$P/logs/xhan-helper-\$$(date +%Y-%m-%d).log"; \
		echo "========================================"; \
		exit 1); \
	echo ""; \
	echo "✅ 部署完成！"

# 仅部署前端
deploy-frontend: build-frontend
	@if [ -f .env ]; then . ./.env; fi; \
	if [ -z "$$DEPLOY_USER" ] || [ -z "$$DEPLOY_HOST" ]; then \
		echo "❌ 请先设置 .env 文件: DEPLOY_USER=xxx DEPLOY_HOST=xxx"; \
		exit 1; \
	fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; \
	echo "========================================"; \
	echo "       部署前端到阿里云服务器"; \
	echo "========================================"; \
	scp -r $(FRONTEND_DIR)/dist/ $$U@$$H:$(SERVER_PATH)/front-end/ ; \
	echo ""; \
	echo "✅ 前端部署完成！"


# 仅上传 + 重启（不重新编译）
deploy-only:
	@if [ -f .env ]; then . ./.env; fi; \
	if [ -z "$$DEPLOY_USER" ] || [ -z "$$DEPLOY_HOST" ]; then \
		echo "❌ 请先设置 .env 文件: DEPLOY_USER=xxx DEPLOY_HOST=xxx"; \
		echo "   参考 .env.example"; \
		exit 1; \
	fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); B=$(OUTPUT_DIR)/$(BINARY); \
	test -f $$B || (echo "❌ 未找到编译产物，请先执行 make build-linux"; exit 1); \
	echo "========================================"; \
	echo "       上传到阿里云服务器 (仅上传)"; \
	echo "========================================"; \
	echo "1/4 上传二进制..."; \
	scp $$B $$U@$$H:$$P/$(BINARY).tmp; \
	echo "2/4 上传配置文件 + 控制脚本..."; \
	scp conf/config.yaml conf/config.prod.yaml $$U@$$H:$$P/conf/; \
	scp scripts/control-xhan-helper.sh $$U@$$H:$$P/; \
	echo "3/4 上传 SQL 迁移文件..."; \
	scp -r migrations/ $$U@$$H:$$P/; \
	echo "4/4 替换二进制并重启..."; \
	ssh $$U@$$H "cd $$P && mv $(BINARY).tmp $(BINARY) && chmod +x $(BINARY) control-xhan-helper.sh && bash control-xhan-helper.sh" || ( \
		echo ""; \
		echo "❌ 启动失败，日志如下："; \
		echo "========================================"; \
		ssh $$U@$$H "tail -50 $$P/logs/xhan-helper-\$$(date +%Y-%m-%d).log"; \
		echo "========================================"; \
		exit 1); \
	echo ""; \
	echo "✅ 部署完成！"

# 实时查看远程日志
logs:
	@if [ -f .env ]; then . ./.env; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; \
	ssh $$U@$$H "tail -f $(SERVER_PATH)/logs/xhan-helper-\$$(date +%Y-%m-%d).log"

# 清理
clean:
	rm -f xhan-helper
	rm -rf $(OUTPUT_DIR)
	rm -rf $(FRONTEND_DIR)/dist
	rm -f data/xhan_helper.db
