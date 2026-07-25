BINARY := gh-sim
PKG := ./cmd/gh-sim
DIST := dist

EXAMPLES := examples/workflows
SCENARIOS := examples/scenarios.txt

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# 配布対象のプラットフォーム
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64

# シナリオ定義からコメントと空行を除いて読む。
# Makefile 内でリテラルの # を書くためにエスケープしている。
SCENARIO_LINES := grep -v '^[[:space:]]*\#' $(SCENARIOS) | grep -v '^[[:space:]]*$$'

# すべてのシナリオに共通で付く引数
DEMO_ARGS := run -dir $(EXAMPLES) -all -notes=false

.PHONY: help all build install test cover vet fmt demo demo-one scenarios e2e e2e-update dist clean

help:
	@echo "使えるターゲット:"
	@echo "  make build        gh-sim をビルドする"
	@echo "  make test         すべてのテストを実行する（サンプルの検証も含む）"
	@echo "  make cover        カバレッジを表示する"
	@echo "  make demo         サンプルに対して全シナリオを実行し結果を表示する"
	@echo "  make demo-one NAME=push-main   1つのシナリオだけ実行する"
	@echo "  make scenarios    使えるシナリオ名を一覧表示する"
	@echo "  make e2e          サンプルの出力が golden と一致するか検証する"
	@echo "  make e2e-update   golden を現在の出力で更新する"
	@echo "  make dist         各プラットフォーム向けバイナリを作る"

all: vet test build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

install:
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

test:
	go test ./...

cover:
	go test -cover ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# サンプルに対して全シナリオを順に実行し、結果を表示する。
demo: build
	@$(SCENARIO_LINES) | while read -r name args; do \
		printf '\n\033[1m### %s\033[0m  (gh-sim run %s)\n' "$$name" "$$args"; \
		./$(BINARY) $(DEMO_ARGS) $$args || exit 1; \
	done

# 1つのシナリオだけ実行する: make demo-one NAME=push-main
demo-one: build
	@if [ -z "$(NAME)" ]; then \
		echo "NAME にシナリオ名を指定してください（例: make demo-one NAME=push-main）"; \
		echo "使えるシナリオは make scenarios で確認できます"; \
		exit 1; \
	fi
	@args=$$($(SCENARIO_LINES) | awk -v n="$(NAME)" '$$1==n {$$1=""; print}'); \
	if [ -z "$$args" ]; then \
		echo "シナリオ $(NAME) が $(SCENARIOS) にありません"; exit 1; \
	fi; \
	printf '\033[1m### %s\033[0m  (gh-sim run %s)\n' "$(NAME)" "$$args"; \
	./$(BINARY) $(DEMO_ARGS) $$args

# 使えるシナリオ名を一覧表示する。
scenarios:
	@$(SCENARIO_LINES) | awk '{name=$$1; $$1=""; printf "  %-20s %s\n", name, substr($$0, 2)}'

# サンプルの出力が golden と一致するか検証する。
e2e:
	go test $(PKG) -run TestExamples -count=1

# golden を現在の出力で更新する。差分を確認してからコミットする。
e2e-update:
	go test $(PKG) -run TestExamples -count=1 -update
	@echo "golden を更新しました。git diff で差分を確認してください。"

# 各プラットフォーム向けのシングルバイナリを dist/ に作る。
# CGO を切って静的リンクにし、単体で配布できる状態にする。
dist: clean
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out=$(DIST)/$(BINARY)_$${os}_$${arch}; \
		if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -trimpath -ldflags '$(LDFLAGS)' -o $$out $(PKG) || exit 1; \
	done
	@echo
	@ls -lh $(DIST)

clean:
	@rm -rf $(DIST) $(BINARY)
