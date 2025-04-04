//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fredwangwang/etw"
)

func main() {
	var (
		optSilent = flag.Bool("silent", false, "Stop sending logs to stderr")
		optHeader = flag.Bool("header", false, "Show event header in output")
		optID     = flag.Int("id", -1, "Capture only specified ID")
	)
	flag.Parse()

	if flag.NArg() != 1 {
		log.Fatalf("Usage: %s [opts] <providerGUID>", filepath.Base(os.Args[0]))
	}
	if *optSilent {
		log.SetOutput(io.Discard)
	}

	guid, err := windows.GUIDFromString(flag.Arg(0))
	if err != nil {
		log.Fatalf("Incorrect GUID given; %s", err)
	}
	session, err := etw.NewSession(guid,
		etw.WithName("go-test-etw-session"),
		etw.WithBufferSize(4),
		etw.WithMinimumBuffers(2),
		etw.WithAdditionalLogFileMode(etw.EVENT_TRACE_NO_PER_PROCESSOR_BUFFERING))
	if err != nil {
		log.Fatalf("Failed to create etw session; %s", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	cb := func(e *etw.Event) {
		if *optID > 0 && *optID != int(e.Header.ID) {
			return
		}

		event := make(map[string]interface{})
		if *optHeader {
			event["Header"] = e.Header
		}
		if data, err := e.EventProperties(); err == nil {
			event["EventProperties"] = data
		} else {
			log.Printf("[ERR] Failed to enumerate event properties: %s", err)
		}
		_ = enc.Encode(event)
	}

	go func() {
		for {
			// keep flushing events every 10ms, hopefully to get event faster than every second.
			time.Sleep(10 * time.Millisecond)
			err := session.FlushEvents()
			if err != nil {
				log.Printf("[ERR] Failed to flush events: %s", err)
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		log.Printf("[DBG] Starting to listen ETW events from %s", guid)

		// Block until .Close().
		if err := session.Process(cb); err != nil {
			log.Printf("[ERR] Got error processing events: %s", err)
		} else {
			log.Printf("[DBG] Successfully shut down")
		}

		wg.Done()
	}()

	// Trap cancellation (the only signal values guaranteed to be present in
	// the os package on all systems are os.Interrupt and os.Kill).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	// Wait for stop and shutdown gracefully.
	for range sigCh {
		log.Printf("[DBG] Shutting the session down")

		err = session.Close()
		if err != nil {
			log.Printf("[ERR] (!!!) Failed to stop session: %s\n", err)
		} else {
			break
		}
	}

	wg.Wait()
}
