MAKEFLAGS := --print-directory
SHELL := bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c

BINARY=nb1
APP_VERSION := v0.0.1

# for go dev
GOCMD=go
GORUN=$(GOCMD) run
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GODOC=$(GOCMD) doc
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# for CircleCI, GitHub Actions, GitLab CI
ifeq ($(origin CIRCLE_BUILD_NUM), environment)
	BUILD_NUM ?= cc$(CIRCLE_BUILD_NUM)
else ifeq ($(origin GITHUB_RUN_NUMBER), environment)
	BUILD_NUM ?= gh$(GITHUB_RUN_NUMBER)
else ifeq ($(origin CI_PIPELINE_IID), environment)
	BUILD_NUM ?= gl$(CI_PIPELINE_IID)
endif

export TZ=Asia/Shanghai
export PACK=github.com/b1ug/nb1/config
export FLAGS="-s -w -X '$(PACK).AppName=$(BINARY)' -X '$(PACK).BuildDate=`date '+%Y-%m-%dT%T%z'`' -X '$(PACK).BuildHost=`hostname`' -X '$(PACK).GoVersion=`go version`' -X '$(PACK).GitBranch=`git symbolic-ref -q --short HEAD`' -X '$(PACK).GitCommit=`git rev-parse --short HEAD`' -X '$(PACK).GitSummary=`git describe --tags --dirty --always`' -X '$(PACK).CIBuildNum=${BUILD_NUM}'"

# for docker
DOCKER ?= docker
DOCKER_REGISTRY ?= ai69
DOCKER_IMG_NAME := $(BINARY)
COMMIT_ID ?= $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date '+%Y%m%d')
ifeq ($(origin BUILD_NUM), undefined)
	BUILD_TAG ?= $(BUILD_DATE)
else
	BUILD_TAG ?= $(BUILD_NUM)
endif
DOCKER_IMG_FULL_NAME := $(DOCKER_REGISTRY)/$(DOCKER_IMG_NAME):$(APP_VERSION)
DOCKER_IMG_FULL_NAME_BUILD := $(DOCKER_REGISTRY)/$(DOCKER_IMG_NAME):$(APP_VERSION)-$(COMMIT_ID)-$(BUILD_TAG)

define safe-push
	@echo "🚀 Push image $(1) to registry '$(DOCKER_REGISTRY)' if not exists"
	@$(DOCKER) manifest inspect $(1) > /dev/null || $(DOCKER) push $(1)
endef

define force-push
	@echo "🚀 Force push image $(1) to registry '$(DOCKER_REGISTRY)'"
	@$(DOCKER) manifest inspect $(1) > /dev/null || echo "$(1) will be overwritten"
	@$(DOCKER) push $(1)
endef

# commands
.PHONY: default run preview clean build build_windows build_mac build_linux install test bench doc build_docker rebuild_docker run_docker bash_docker publish_docker artifact
default:
	@echo "build target is required for $(BINARY)"
	@exit 1

# basic commands
run:
	$(GORUN) . v
preview:
	./$(BINARY) v
	./$(BINARY) ls
clean:
	rm -f $(BINARY)*

# for go dev
build:
	$(GOBUILD) -v -ldflags $(FLAGS) -trimpath -o $(BINARY)
build_windows:
	GOOS=windows GOARCH=amd64 $(GOBUILD) -v -ldflags $(FLAGS) -trimpath -o $(BINARY).exe
build_mac:
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -v -ldflags $(FLAGS) -trimpath -o $(BINARY)
build_linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -v -ldflags $(FLAGS) -trimpath -o $(BINARY)
install: build
	mv $(BINARY) $$GOPATH/bin
test:
	$(GOTEST) -race -cover -covermode=atomic -v -count 1 .
bench:
	$(GOTEST) -parallel=4 -run="none" -benchtime="5s" -benchmem -bench=.
doc:
	$(GODOC) -all ...

# for docker dev
build_docker: build_linux
	$(DOCKER) build --build-arg BINARY=$(BINARY) -t $(DOCKER_IMG_NAME) .

rebuild_docker: build_linux
	$(DOCKER) build --pull --no-cache --build-arg BINARY=$(BINARY) -t $(DOCKER_IMG_NAME) .

run_docker:
	$(DOCKER) run --rm -t -p 8080:80 -e PORT=80 --name "$(DOCKER_IMG_NAME)_app" $(DOCKER_IMG_NAME)

bash_docker:
	$(DOCKER) run --rm -it -p 8080:80 -e PORT=80 --name "$(DOCKER_IMG_NAME)_app" $(DOCKER_IMG_NAME) bash

publish_docker:
	$(DOCKER) tag $(DOCKER_IMG_NAME) $(DOCKER_IMG_FULL_NAME)
	$(DOCKER) tag $(DOCKER_IMG_NAME) $(DOCKER_IMG_FULL_NAME_BUILD)
	$(call safe-push,$(DOCKER_IMG_FULL_NAME))
	$(call force-push,$(DOCKER_IMG_FULL_NAME_BUILD))

# for ci
artifact:
	test -n "$(OSEXT)"
	mkdir -p _upload
	cp -f $(BINARY) _upload/$(BINARY).$(OSEXT)
