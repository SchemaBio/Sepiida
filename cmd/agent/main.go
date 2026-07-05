package main

import (
	"context"
	"flag"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/agent/archiver"
	"github.com/SchemaBio/Sepiida/internal/agent/collector"
	"github.com/SchemaBio/Sepiida/internal/agent/parser"
	"github.com/SchemaBio/Sepiida/internal/agent/sender"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
)

func main() {
	// Command line flags
	serverURL := flag.String("s", firstNonEmptyEnv("SEPIIDA_SERVER_URL", "SEPIIDA_API_URL", "SEPIIDA_SERVER"), "server URL; env: SEPIIDA_SERVER_URL")
	apiKey := flag.String("key", os.Getenv("SEPIIDA_AGENT_KEY"), "API key for authentication; env: SEPIIDA_AGENT_KEY")
	agentID := flag.String("id", firstNonEmptyEnv("SEPIIDA_AGENT_ID", "HOSTNAME"), "agent identifier; env: SEPIIDA_AGENT_ID")
	interval := flag.Int("i", parsePositiveIntEnv("SEPIIDA_AGENT_INTERVAL", 60), "poll interval in seconds; env: SEPIIDA_AGENT_INTERVAL")
	watchDirs := flag.String("w", os.Getenv("SEPIIDA_WATCH_DIRS"), "watch directories (comma separated, should contain UUID directories); env: SEPIIDA_WATCH_DIRS")
	archivePath := flag.String("archive", os.Getenv("SEPIIDA_ARCHIVE_URL"), "archive destination (local path, s3://, oss://, cos://, or http(s)://minio); env: SEPIIDA_ARCHIVE_URL")
	archiveKeyID := flag.String("archive-key-id", os.Getenv("SEPIIDA_ARCHIVE_KEY_ID"), "access key ID for object storage (overrides provider env vars); env: SEPIIDA_ARCHIVE_KEY_ID")
	archiveKeySecret := flag.String("archive-key-secret", os.Getenv("SEPIIDA_ARCHIVE_KEY_SECRET"), "secret access key for object storage (overrides provider env vars); env: SEPIIDA_ARCHIVE_KEY_SECRET")
	archiveTimeout := flag.Duration("archive-timeout", defaultArchiveTimeout(), "archive timeout (for example 30m or 2h; env SEPIIDA_ARCHIVE_TIMEOUT)")
	taskToken := flag.String("task-token", firstNonEmptyEnv("SEPIIDA_TASK_TOKEN", "SEPIIDA_AGENT_TASK_TOKEN"), "pre-issued per-task write token; env: SEPIIDA_TASK_TOKEN")
	taskTokenSecret := flag.String("task-token-secret", os.Getenv("SEPIIDA_TASK_TOKEN_SECRET"), "legacy shared secret for locally signing per-task write tokens")
	flag.Parse()

	// Parse watch directories
	dirs := parseWatchDirs(*watchDirs)
	if len(dirs) == 0 {
		dirs = []string{"./output"}
		log.Println("Warning: using default watch directory ./output. Please specify -w for production use.")
	}

	// Validate authentication configuration
	if *serverURL == "" {
		*serverURL = "http://localhost:9090"
	}
	if *agentID == "" {
		*agentID = "agent-001"
	}
	if *apiKey == "" && *taskToken == "" && *taskTokenSecret == "" {
		log.Fatal("Error: no API key, task token, or legacy task token secret specified. Please specify -key, -task-token, or -task-token-secret for authentication.")
	}
	if *taskTokenSecret != "" && len(*taskTokenSecret) < tasktoken.MinSecretBytes {
		log.Fatalf("Error: -task-token-secret must be at least %d characters. Use a long random shared secret.", tasktoken.MinSecretBytes)
	}
	if *interval <= 0 {
		log.Fatal("Error: -i poll interval must be greater than 0 seconds.")
	}

	// Parse poll interval
	pollInterval := time.Duration(*interval) * time.Second

	log.Printf("Starting Sepiida Agent: %s", *agentID)
	log.Printf("Server URL: %s", redactURLForLog(*serverURL))
	log.Printf("Poll Interval: %v", pollInterval)
	log.Printf("Watch Dirs: %v", dirs)
	if *taskToken != "" {
		log.Printf("Authentication: pre-issued task token")
	} else if *taskTokenSecret != "" {
		log.Printf("Authentication: legacy local task-token signing (prefer -task-token for production)")
	} else {
		log.Printf("Authentication: static agent key")
	}

	// Create components
	logParser := parser.NewLogParser()
	progressCollector := collector.NewProgressCollector(logParser, dirs, *agentID)
	httpSender := sender.NewHTTPSenderWithTaskCredential(*serverURL, *apiKey, *agentID, *taskToken, *taskTokenSecret)

	// Create archiver if archive path is specified
	var arch *archiver.Archiver
	if *archivePath != "" {
		var err error
		arch, err = archiver.NewFromPath(*archivePath, *archiveKeyID, *archiveKeySecret)
		if err != nil {
			log.Fatalf("Failed to initialize archiver: %v", err)
		}
		defer arch.Close()
		log.Printf("Archive destination: %s", redactURLForLog(*archivePath))
		log.Printf("Archive timeout: %v", *archiveTimeout)
	}
	progressCollector.SetArchiveEnabled(arch != nil)

	// Start polling loop
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Run first collection immediately
	runCollection(progressCollector, httpSender, arch, *archiveTimeout)

	// Then run on interval
	for range ticker.C {
		runCollection(progressCollector, httpSender, arch, *archiveTimeout)
	}
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func parsePositiveIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Printf("Warning: invalid %s=%q, using %d", name, raw, fallback)
		return fallback
	}
	return value
}

func defaultArchiveTimeout() time.Duration {
	const fallback = 30 * time.Minute

	raw := strings.TrimSpace(os.Getenv("SEPIIDA_ARCHIVE_TIMEOUT"))
	if raw == "" {
		return fallback
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("Warning: invalid SEPIIDA_ARCHIVE_TIMEOUT=%q, using %v", raw, fallback)
		return fallback
	}
	if timeout <= 0 {
		log.Printf("Warning: non-positive SEPIIDA_ARCHIVE_TIMEOUT=%q, using %v", raw, fallback)
		return fallback
	}
	return timeout
}

func parseWatchDirs(dirStr string) []string {
	if dirStr == "" {
		return nil
	}

	// Support comma separated directories
	dirs := strings.Split(dirStr, ",")

	// Clean up paths (remove quotes if present)
	cleaned := make([]string, 0, len(dirs))
	for i, dir := range dirs {
		dirs[i] = strings.Trim(strings.TrimSpace(dir), "\"'")
		if dirs[i] != "" {
			cleaned = append(cleaned, dirs[i])
		}
	}

	return cleaned
}

func redactURLForLog(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	if u.User != nil {
		username := u.User.Username()
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(username, "xxxxx")
		} else {
			u.User = url.User(username)
		}
	}
	query := u.Query()
	changed := false
	for _, key := range []string{"access_key", "access_key_id", "accesskey", "secret", "secret_key", "secret_access_key", "token", "password", "pass", "pwd"} {
		if query.Has(key) {
			query.Set(key, "xxxxx")
			changed = true
		}
	}
	if changed {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func runCollection(collector *collector.ProgressCollector, sender *sender.HTTPSender, arch *archiver.Archiver, archiveTimeout time.Duration) {
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
			if err := collector.SaveState(result.UUIDDir, result.State); err != nil {
				log.Printf("Failed to save push state for UUID %s: %v", uuid, err)
			}

			// Mark outputs as pushed if workflow is done and outputs were sent
			if result.Progress.Workflow.Status == "success" && result.Progress.Workflow.OutputsJSON != "" {
				if err := sender.SendOutput(uuid, workflowID, result.Progress.Workflow.OutputsJSON); err != nil {
					log.Printf("Failed to send output for UUID %s: %v", uuid, err)
				} else {
					log.Printf("Successfully sent output for UUID %s", uuid)
					if err := collector.MarkOutputsPushed(result.UUIDDir); err != nil {
						log.Printf("Failed to mark outputs pushed: %v", err)
					}
				}
			}
		}

		// Archive if configured — independent of server push
		if arch != nil && result.Progress.Workflow.Status == "success" {
			state, _ := collector.LoadState(result.UUIDDir)
			if state != nil && !state.Archived {
				ctx, cancel := context.WithTimeout(context.Background(), archiveTimeout)
				archiveResult, err := arch.ArchiveWorkflow(ctx, uuid, workflowID, result.ExecutionDir)
				cancel()
				if err != nil {
					log.Printf("Failed to archive for UUID %s: %v", uuid, err)
				} else {
					log.Printf("Successfully archived %d items for UUID %s", archiveResult.ArchivedCount, uuid)
					// Notify server that archiving is complete
					if err := sender.NotifyArchived(archiveResult); err != nil {
						log.Printf("WARNING: failed to notify server of archive for UUID %s: %v", uuid, err)
					} else if err := collector.MarkArchived(result.UUIDDir); err != nil {
						log.Printf("Failed to mark archived: %v", err)
					}
				}
			}
		}
	}

	if pushedCount > 0 {
		log.Printf("Pushed %d workflow updates", pushedCount)
	}
}
