.PHONY: build build-app clean help
.DEFAULT_GOAL := help

help: ## Show this help message
	@echo "Growud Make Commands"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

test: ## Run golang tests
	go test ./...

build: ## Build CLI binary
	go build -o growud .

build-app: ## Build macOS .app bundle
	mkdir -p Growud.app/Contents/MacOS
	mkdir -p Growud.app/Contents/Resources
	CGO_ENABLED=1 go build -o Growud.app/Contents/MacOS/growud .
	cp Info.plist Growud.app/Contents/
	cp growud.icns Growud.app/Contents/Resources/

install: build-app ## Install .app to /Applications
	cp -r Growud.app /Applications/

clean: ## Clean build artifacts
	rm -f growud
	rm -rf Growud.app
