.PHONY: dev dev-full local local-build check-env build clean test build-linux build-frontend deploy deploy-backend deploy-frontend deploy-only help logs db-pull db-import entity-gc build-entity-gc topic-merge-plan topic-merge-apply build-topic-merge restart

# 远端 ssh 执行时 source 用户环境变量（非交互 ssh 不会自动 source）
REMOTE_ENV = for f in ~/.zshrc ~/.zprofile ~/.bashrc ~/.bash_profile; do source "$$f" 2>/dev/null || true; done;

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
	@echo "  make dev            - 本地开发模式启动（dev环境，使用MySQL）"
	@echo "  make local          - 本地部署模式启动（local环境，使用SQLite）"
	@echo "  make local-build    - 编译本地版二进制（app 打包用）"
	@echo "  make dev-full       - 同时启动前后端开发服务器"
	@echo "  make build          - 本地编译（当前系统）"
	@echo "  make build-topic-fix - 编译+上传 topic 修复工具（修复 memory 中含 / 或 , 的脏 topic）"
	@echo "  make build-backfill - 编译+上传概念回填工具（只跑 topic/tags → concept embedding）"
	@echo "  make build-rebuild  - 编译+上传记忆重建工具（全量重建）"
	@echo "  make build-linux    - 交叉编译 Linux amd64 版本（用于阿里云服务器）"
	@echo "  make build-frontend - 构建前端静态文件"
	@echo "  make deploy         - 一键部署：编译前后端 + 上传 + 重启"
	@echo "  make deploy-backend - 仅部署后端（编译 + 上传 + 重启）"
	@echo "  make deploy-frontend- 仅部署前端（构建 + 上传）"
	@echo "  make deploy-only    - 仅上传已编译的二进制和conf（不重新编译）"
	@echo "  make db-pull        - 导出线上数据库并拉到本地 data/ 目录"
	@echo "  make db-import      - 仅导入本地最新 dump（不联网，用于下载后导入失败重跑）"
	@echo "  make entity-gc      - 远端清理孤儿派生数据（默认 dry-run；ARGS=\"--write\" 落库）"
	@echo "  make topic-merge-plan  - 远端生成 topic 收敛报告+映射，拉到本地 data/topic-merge/"
	@echo "  make topic-merge-apply - 推回本地映射并在远端执行（默认 dry-run；ARGS=\"--dry-run=false\" 落库，自动停服/起服）"
	@echo "  make restart        - 重启远端服务"
	@echo "  make logs           - 实时查看远程服务器日志 (tail -f)"
	@echo "  make clean          - 清理编译产物"

# 启动开发服务器
dev: check-env
	@echo "=== 启动 xhanHelper 开发服务器 (dev → MySQL) ==="
	@mkdir -p logs
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	go run ./cmd/xhan-helper/ 2>&1 | tee logs/dev-$$(date +%Y-%m-%d).log

# 启动本地版服务（local → SQLite @ Application Support）
local: check-env
	@echo "=== 启动本地服务 (local → SQLite @ ~/Library/Application Support/xhanHelper) ==="
	@mkdir -p logs
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	APP_ENV=local go run ./cmd/xhan-helper/ 2>&1 | tee logs/local-$$(date +%Y-%m-%d).log

# 编译本地版二进制（app 打包用）
local-build:
	@echo "=== 编译本地版二进制 ==="
	@mkdir -p $(OUTPUT_DIR)
	go build -o $(OUTPUT_DIR)/$(BINARY) ./cmd/xhan-helper/

# 同时启动前后端开发服务器
dev-full: check-env
	@echo "=== 启动前后端开发环境 ==="
	@mkdir -p logs
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	LOG="logs/dev-$$(date +%Y-%m-%d).log"; \
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
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	ok=true; \
	if [ -z "$$LLM_API_KEY" ]; then \
		echo "❌ LLM_API_KEY 未设置"; \
		echo "   export LLM_API_KEY=\"your-doubao-api-key\""; \
		ok=false; \
	fi; \
	if [ "$$APP_ENV" = "prod" ] && [ -z "$$XHAN_HELPER_DB_PASSWORD" ]; then \
		echo "❌ XHAN_HELPER_DB_PASSWORD 未设置 (prod 环境需要)"; \
		echo "   在 server/.env 或环境变量里设置"; \
		ok=false; \
	fi; \
	if [ -z "$$EMBEDDING_API_KEY" ]; then \
		echo "💡 EMBEDDING_API_KEY 未设置，向量检索功能不可用"; \
		echo "   export EMBEDDING_API_KEY=\"your-ark-api-key\""; \
	fi; \
	if ! $$ok; then \
		echo ""; \
		echo "环境变量检查未通过，请补齐后重试"; \
		exit 1; \
	fi; \
	if [ -z "$$APP_ENV" ]; then \
		echo "💡 APP_ENV 未设置，默认使用 dev (MySQL)"; \
	fi; \
	echo "✅ 环境变量检查通过"

# API 自动化测试
# test target 暂时停用（脚本/测试库长期不可用）。启用前请先修复 scripts/api-test.sh 与测试数据库残留问题。
# test: check-env
# 	@echo "=== 单元测试 ==="
# 	@go test ./... -timeout 30s || exit 1
# 	@echo "=== 编译 ==="
# 	@go build -o /tmp/xhan-test-server ./cmd/xhan-helper/ || exit 1
# 	@echo "=== 备份数据库 ==="
# 	@mkdir -p data
# 	@if [ -f data/xhan_helper.db ]; then \
# 		mv data/xhan_helper.db data/xhan_helper.db.backup; \
# 		echo "   ✓ 已备份: data/xhan_helper.db.backup"; \
# 	fi
# 	@echo "=== 启动测试服务器 (APP_ENV=test, SQLite) ==="
# 	@APP_ENV=test /tmp/xhan-test-server &
# 	@sleep 2
# 	@echo "=== 运行 API 测试 ==="
# 	@bash scripts/api-test.sh; EXIT=$$?
# 	@echo ""
# 	@echo "=== 停止测试服务器 ==="
# 	@kill $$(lsof -ti:8080) 2>/dev/null || true
# 	@rm -f /tmp/xhan-test-server
# 	@echo "=== 恢复数据库 ==="
# 	@if [ -f data/xhan_helper.db.backup ]; then \
# 		mv data/xhan_helper.db.backup data/xhan_helper.db; \
# 		echo "   ✓ 已恢复"; \
# 	else \
# 		rm -f data/xhan_helper.db; \
# 		echo "   ✓ 已清理测试数据库"; \
# 	fi
# 	@exit $$EXIT

# 导出线上数据库并拉到本地
db-pull:
	@if [ -f .env ]; then . ./.env; fi; \
	if [ -z "$$DEPLOY_USER" ] || [ -z "$$DEPLOY_HOST" ]; then \
		echo "❌ 请先设置 .env 文件: DEPLOY_USER=xxx DEPLOY_HOST=xxx"; \
		echo "   参考 .env.example"; \
		exit 1; \
	fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	echo "========================================"; \
	echo "       导出线上数据库"; \
	echo "========================================"; \
	echo "1/2 上传 dump 脚本 + 远程执行导出（临时目录）..."; \
	scp scripts/dump-db.sh $$U@$$H:$$P/scripts/dump-db.sh; \
	ssh $$U@$$H "chmod +x $$P/scripts/dump-db.sh && cd $$P && bash scripts/dump-db.sh /tmp/xhan-helper-dump 1"; \
	echo ""; \
	echo "2/2 拉取最新 dump 到本地..."; \
	bash ../../mysh/pull-mysqldump.sh

# 仅导入本地最新 dump（不联网）
db-import:
	@echo "========================================"
	@echo "       仅导入本地最新 dump（不联网）"
	@echo "========================================"
	@LOCAL_ONLY=1 bash ../../mysh/pull-mysqldump.sh

# 编译并上传 entity-gc 工具
build-entity-gc:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	mkdir -p $(OUTPUT_DIR); \
	GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/entity-gc ./cmd/entity-gc/; \
	scp $(OUTPUT_DIR)/entity-gc $$U@$$H:$$P/entity-gc.tmp; \
	ssh $$U@$$H "cd $$P && mv entity-gc.tmp entity-gc && chmod +x entity-gc"; \
	echo "✅ 已上传 entity-gc"

# 远端清理孤儿派生数据（默认 dry-run；ARGS="--write" 落库）
entity-gc: build-entity-gc
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	ssh $$DEPLOY_USER@$$DEPLOY_HOST '$(REMOTE_ENV) cd $(SERVER_PATH) && APP_ENV=prod ./entity-gc $(ARGS)'

# 编译并上传 topic-merge 工具 + 远端执行脚本
build-topic-merge:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	mkdir -p $(OUTPUT_DIR); \
	GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/topic-merge ./cmd/topic-merge/; \
	scp $(OUTPUT_DIR)/topic-merge $$U@$$H:$$P/topic-merge.tmp; \
	scp scripts/run-topic-merge.sh $$U@$$H:$$P/scripts/run-topic-merge.sh; \
	ssh $$U@$$H "cd $$P && mv topic-merge.tmp topic-merge && chmod +x topic-merge scripts/run-topic-merge.sh"; \
	echo "✅ 已上传 topic-merge"

# 远端生成报告+映射（只读），拉回本地 data/topic-merge/
topic-merge-plan: build-topic-merge
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	ssh $$U@$$H "cd $$P && bash scripts/run-topic-merge.sh $(ARGS)" && \
	mkdir -p data/topic-merge && \
	scp -r $$U@$$H:$$P/data/topic-merge/. ./data/topic-merge/ && \
	echo "✅ 报告/映射已拉到 data/topic-merge/（编辑 plan-*.tsv 后跑 make topic-merge-apply）"

# 推回本地编辑后的映射并在远端执行（默认 dry-run；ARGS="--dry-run=false" 落库，自动停服/起服）
topic-merge-apply: build-topic-merge
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	ls ./data/topic-merge/*.tsv >/dev/null 2>&1 || { \
		echo "❌ 本地没有 data/topic-merge/*.tsv"; \
		echo "   请先跑 make topic-merge-plan，审阅 report、编辑 plan-*.tsv 后再 apply"; \
		exit 1; }; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	scp ./data/topic-merge/*.tsv $$U@$$H:$$P/data/topic-merge/ && \
	ssh $$U@$$H "cd $$P && bash scripts/run-topic-merge.sh --apply $(ARGS)"

# 重启远端服务
restart:
	@if [ -f .env ]; then . ./.env; fi; \
	ssh $$DEPLOY_USER@$$DEPLOY_HOST "cd $(SERVER_PATH) && bash scripts/control-xhan-helper.sh"

# 编译生产二进制
build:
	go build -o xhan-helper ./cmd/xhan-helper/

# 编译 topic 修复工具 (Linux amd64)
build-topic-fix:
	@if [ -f .env ]; then . ./.env; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	mkdir -p $(OUTPUT_DIR); \
	GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/topic-fix ./cmd/topic-fix/; \
	scp $(OUTPUT_DIR)/topic-fix $$U@$$H:$$P/topic-fix.tmp; \
	ssh $$U@$$H "cd $$P && mv topic-fix.tmp topic-fix && chmod +x topic-fix"; \
	echo "✅ 上传完成，SSH 上去运行: APP_ENV=prod LLM_API_KEY=xxx ./topic-fix"

# 编译概念回填工具 (Linux amd64)
build-backfill:
	@if [ -f .env ]; then . ./.env; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	mkdir -p $(OUTPUT_DIR); \
	GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/concept-backfill ./cmd/concept-backfill/; \
	scp $(OUTPUT_DIR)/concept-backfill $$U@$$H:$$P/concept-backfill.tmp; \
	ssh $$U@$$H "cd $$P && mv concept-backfill.tmp concept-backfill && chmod +x concept-backfill"; \
	echo "✅ 上传完成，SSH 上去运行: APP_ENV=prod LLM_API_KEY=xxx EMBEDDING_API_KEY=xxx ./concept-backfill"

# 编译记忆重建工具 (Linux amd64)
build-rebuild:
	@if [ -f .env ]; then . ./.env; fi; \
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); \
	mkdir -p $(OUTPUT_DIR); \
	GOOS=linux GOARCH=amd64 go build -o $(OUTPUT_DIR)/memory-rebuild ./cmd/memory-rebuild/; \
	scp $(OUTPUT_DIR)/memory-rebuild $$U@$$H:$$P/memory-rebuild.tmp; \
	ssh $$U@$$H "cd $$P && mv memory-rebuild.tmp memory-rebuild && chmod +x memory-rebuild"; \
	echo "✅ 上传完成，SSH 上去运行: APP_ENV=prod LLM_API_KEY=xxx EMBEDDING_API_KEY=xxx ./memory-rebuild"

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
	U=$$DEPLOY_USER; H=$$DEPLOY_HOST; P=$(SERVER_PATH); B=$(OUTPUT_DIR)/$(BINARY); F=$(FRONTEND_DIR)/dist; \
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
	scp scripts/control-xhan-helper.sh $$U@$$H:$$P/scripts/; \
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
	scp scripts/control-xhan-helper.sh $$U@$$H:$$P/scripts/; \
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
	scp -r $(FRONTEND_DIR)/dist $$U@$$H:$(SERVER_PATH)/front-end/ ; \
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
	scp scripts/control-xhan-helper.sh $$U@$$H:$$P/scripts/; \
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
