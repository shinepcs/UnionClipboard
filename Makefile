APP_NAME := UnionClipboard
DIST_DIR := dist
MAC_APP := $(DIST_DIR)/$(APP_NAME).app
WIN_EXE := $(DIST_DIR)/$(APP_NAME).exe

.PHONY: all test build build-mac build-win clean

all: test build

test:
	go test -race ./...

build: build-mac build-win

build-mac:
	@mkdir -p $(MAC_APP)/Contents/MacOS $(MAC_APP)/Contents/Resources
	CGO_ENABLED=1 go build -ldflags="-s -w" -o $(MAC_APP)/Contents/MacOS/$(APP_NAME) ./cmd/unionclip
	cp packaging/macos/Info.plist $(MAC_APP)/Contents/Info.plist
	codesign --force --deep --sign - $(MAC_APP)

build-win:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o $(WIN_EXE) ./cmd/unionclip

clean:
	rm -rf $(DIST_DIR)
