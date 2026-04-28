package main

import (
	"flag"
	"log"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/agent/parser"
	"github.com/SchemaBio/Sepiida/internal/agent/collector"
	"github.com/SchemaBio/Sepiida/internal/agent/sender"
)

func main() {
	// Command line flags
	serverURL := flag.String("s", "http://localhost:8080", "server URL")
	apiKey := flag.String("key", "", "API key for authentication")
	agentID := flag.String("id", "agent-001", "agent identifier")
	interval := flag.Int("i", 60, "poll interval in seconds")
	watchDirs := flag.String("w", "", "watch directories (comma separated, should contain UUID directories)")
	flag.Parse()

	// Parse watch directories
	dirs := parseWatchDirs(*watchDirs)
	if len(dirs) == 0 {
		dirs = []string{"./output"}
		log.Println("Warning: using default watch directory ./output. Please specify -w for production use.")
	}

	// Validate API key
	if *apiKey == "" {
		log.Println("Warning: no API key specified. Please specify -key for authentication.")
	}

	// Parse poll interval
	pollInterval := time.Duration(*interval) * time.Second

	log.Printf("Starting Sepiida Agent: %s", *agentID)
	log.Printf("Server URL: %s", *serverURL)
	log.Printf("Poll Interval: %v", pollInterval)
	log.Printf("Watch Dirs: %v", dirs)

	// Create components
	logParser := parser.NewLogParser()
	progressCollector := collector.NewProgressCollector(logParser, dirs, *agentID)
	httpSender := sender.NewHTTPSender(*serverURL, *apiKey)

	// Start polling loop
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Run first collection immediately
	runCollection(progressCollector, httpSender)

	// Then run on interval
	for range ticker.C {
		runCollection(progressCollector, httpSender)
	}
}

func parseWatchDirs(dirStr string) []string {
	if dirStr == "" {
		return nil
	}

	// Support comma separated directories
	dirs := strings.Split(dirStr, ",")

	// Clean up paths (remove quotes if present)
	for i, dir := range dirs {
		dirs[i] = strings.Trim(strings.TrimSpace(dir), "\"'")
	}

	return dirs
}

func runCollection(collector *collector.ProgressCollector, sender *sender.HTTPSender) {
	log.Println("Collecting workflow progress...")

	results, err := collector.Collect()
	if err != nil {
		log.Printf("Failed to collect progress: %v", err)
		return
	}

	if len(results) == 0 {
		log.Println("No workflows found or no changes to push")
		return
	}

	pushedCount := 0
	for _, result := range results {
		if !result.NeedPush {
			continue
		}

		uuid := result.Progress.UUID
		workflowID := result.Progress.Workflow.ID
		log.Printf("Sending progress: UUID=%s, Workflow=%s, Status=%s", uuid, workflowID, result.Progress.Workflow.Status)

		if err := sender.SendProgress(&result.Progress); err != nil {
			log.Printf("Failed to send progress for UUID %s: %v", uuid, err)
		} else {
			log.Printf("Successfully sent progress for UUID %s", uuid)
			pushedCount++

			// Mark outputs as pushed if workflow is done and outputs were sent
			if result.Progress.Workflow.Status == "success" && result.Progress.Workflow.OutputsJSON != "" {
				if err := sender.SendOutput(uuid, workflowID, result.Progress.Workflow.OutputsJSON); err != nil {
					log.Printf("Failed to send output for UUID %s: %v", uuid, err)
				} else {
					log.Printf("Successfully sent output for UUID %s", uuid)
					// Mark outputs as pushed in state file (in UUID directory)
					if err := collector.MarkOutputsPushed(result.UUIDDir); err != nil {
						log.Printf("Failed to mark outputs pushed: %v", err)
					}
				}
			}
		}
	}

	if pushedCount > 0 {
		log.Printf("Pushed %d workflow updates", pushedCount)
	}
}