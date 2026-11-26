package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"unionclip/internal/app"
)

func main() {
	runtime.LockOSThread()

	dataDir := flag.String("data-dir", "", "Override application data directory")
	port := flag.Int("port", -1, "Override P2P sync listen port")
	openViewer := flag.Bool("open-viewer", true, "Open viewer window on startup")
	background := flag.Bool("background", false, "Start minimized to tray/menubar without opening viewer")
	headlessCheck := flag.Bool("headless-check", false, "Initialize core services, print endpoints, and exit")
	flag.Parse()

	application, err := app.New(*dataDir, *port)
	if err != nil {
		log.Fatalf("UnionClipboard init error: %v", err)
	}

	if *headlessCheck {
		fmt.Printf("OK sync_port=%d viewer_url=%s\n", application.SyncPort(), application.ViewerURL())
		application.Shutdown()
		return
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		application.Shutdown()
		os.Exit(0)
	}()

	showViewer := *openViewer && !*background
	if err := application.Run(showViewer); err != nil {
		log.Fatalf("UnionClipboard run error: %v", err)
	}
}
