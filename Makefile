APP_NAME := unexcel
APP_VERSION := $(shell git describe --tags --dirty --always)
MAIN_SRC := ./unexcel.go
BUILD_DIR := ./bin
LDFLAGS := -ldflags="-s -w -X main.version=$(APP_VERSION)"

LOCAL_GOOS := $(shell go env GOOS)
LOCAL_GOARCH := $(shell go env GOARCH)

PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: build all clean $(PLATFORMS)

.DEFAULT_GOAL := build

build: $(LOCAL_GOOS)/$(LOCAL_GOARCH)

all: $(PLATFORMS)

$(PLATFORMS):
	@$(eval GOOS := $(word 1,$(subst /, ,$@)))
	@$(eval GOARCH := $(word 2,$(subst /, ,$@)))
	@$(eval EXT := $(if $(filter windows,$(GOOS)),.exe,))
	@echo "Building for $(GOOS)/$(GOARCH)..."
	@CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(LDFLAGS) -o $(BUILD_DIR)/$(GOOS)-$(GOARCH)/$(APP_NAME)$(EXT) $(MAIN_SRC)

clean:
	@rm -rf $(BUILD_DIR)
