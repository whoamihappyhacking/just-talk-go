.PHONY: build test clean run

APP_NAME := just-talk
CMD_DIR := ./cmd/just-talk
BUILD_DIR := ./build

# Build for current platform
build:
	go build -o $(BUILD_DIR)/$(APP_NAME) $(CMD_DIR)

# Run (current platform)
run:
	go run $(CMD_DIR)

# Test
test:
	go test ./... -v

# Clean
clean:
	rm -rf $(BUILD_DIR)

# Install dependencies
deps:
	go mod tidy
	go mod download
