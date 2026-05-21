package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/apikey"
	"github.com/SchemaBio/Sepiida/internal/common/db"
	"github.com/SchemaBio/Sepiida/internal/server/handler"
	"github.com/SchemaBio/Sepiida/internal/server/middleware"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

func main() {
	// Command line flags
	port := flag.String("p", "8080", "server listen port")
	database := flag.String("d", "postgres://localhost:5432/sepiida?user=postgres&password=postgres", "database connection string (postgres://host:port/db?user=xxx&password=xxx)")
	agentKeyFile := flag.String("agent-key", "", "path to agent key file (keys for pushing data)")
	queryKeyFile := flag.String("query-key", "", "path to query key file (keys for querying results)")
	keyRefresh := flag.Int("key-refresh", 30, "key file refresh interval in seconds")
	flag.Parse()

	// Validate key files
	if *agentKeyFile == "" {
		log.Fatal("Error: -agent-key parameter is required. Please specify an agent key file path.")
	}
	if *queryKeyFile == "" {
		log.Fatal("Error: -query-key parameter is required. Please specify a query key file path.")
	}

	// Initialize multi key manager
	mkm := apikey.NewMultiKeyManager(*agentKeyFile, *queryKeyFile)
	mkm.Start(time.Duration(*keyRefresh) * time.Second)

	// Wait for initial key load
	time.Sleep(100 * time.Millisecond)

	agentKeyCount := mkm.AgentKeyCount()
	queryKeyCount := mkm.QueryKeyCount()

	if agentKeyCount == 0 {
		log.Printf("Warning: No agent keys loaded from %s", *agentKeyFile)
	} else {
		log.Printf("Loaded %d agent keys from %s", agentKeyCount, *agentKeyFile)
	}

	if queryKeyCount == 0 {
		log.Printf("Warning: No query keys loaded from %s", *queryKeyFile)
	} else {
		log.Printf("Loaded %d query keys from %s", queryKeyCount, *queryKeyFile)
	}

	// Parse database connection
	cfg := parsePostgresConn(*database)

	// Initialize database
	databaseObj, err := db.NewPostgreSQL(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer databaseObj.Close()

	// Initialize database tables
	if err := databaseObj.Initialize(context.Background()); err != nil {
		log.Fatalf("Failed to initialize database tables: %v", err)
	}

	// Create service and handler
	workflowService := service.NewWorkflowService(databaseObj)
	progressHandler := handler.NewProgressHandler(workflowService)

	// Create authentication middleware
	agentAuth := middleware.NewAgentAuthMiddleware(mkm.GetAgentKeyManager())
	queryAuth := middleware.NewQueryAuthMiddleware(mkm.GetQueryKeyManager())

	// Setup routes
	router := http.NewServeMux()

	// Agent API routes (push data) - use agent auth
	router.Handle("/api/v1/progress", agentAuth.Middleware(http.HandlerFunc(progressHandler.HandleProgress)))
	router.Handle("/api/v1/workflow/output", agentAuth.Middleware(http.HandlerFunc(progressHandler.HandleOutput)))

	// Archive notification - agent auth (agent reports archive completion)
	router.Handle("/api/v1/workflow/archive", agentAuth.Middleware(http.HandlerFunc(progressHandler.HandleArchive)))

	// Query API routes - use query auth
	router.Handle("/api/v1/workflow", queryAuth.Middleware(http.HandlerFunc(progressHandler.HandleGetWorkflow)))
	router.Handle("/api/v1/workflow/tasks", queryAuth.Middleware(http.HandlerFunc(progressHandler.HandleGetWorkflowTasks)))
	router.Handle("/api/v1/workflows", queryAuth.Middleware(http.HandlerFunc(progressHandler.HandleListWorkflows)))

	// Keys management API - use query auth (requires query key to access)
	keysHandler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/keys/status":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "agent_keys_file: %s\nagent_keys_count: %d\nquery_keys_file: %s\nquery_keys_count: %d\nrefresh_interval: %ds\n",
				*agentKeyFile, mkm.AgentKeyCount(), *queryKeyFile, mkm.QueryKeyCount(), *keyRefresh)

		case "/api/v1/keys/reload":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			mkm.ReloadAll()
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Reloaded keys.\nAgent keys: %d from %s\nQuery keys: %d from %s\n",
				mkm.AgentKeyCount(), *agentKeyFile, mkm.QueryKeyCount(), *queryKeyFile)

		default:
			http.NotFound(w, r)
		}
	}
	router.Handle("/api/v1/keys/", queryAuth.Middleware(http.HandlerFunc(keysHandler)))

	// Health check endpoint (no authentication required)
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Start server
	listenAddr := ":" + *port
	log.Printf("Starting Sepiida Server on %s", listenAddr)
	log.Printf("Database: %s", *database)
	log.Printf("Agent Key File: %s (%d keys)", *agentKeyFile, agentKeyCount)
	log.Printf("Query Key File: %s (%d keys)", *queryKeyFile, queryKeyCount)

	if err := http.ListenAndServe(listenAddr, router); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func parsePostgresConn(conn string) db.Config {
	// Strip postgres:// prefix
	conn = strings.TrimPrefix(conn, "postgres://")

	parts := strings.SplitN(conn, "?", 2)
	hostPortDb := parts[0]
	params := ""
	if len(parts) > 1 {
		params = parts[1]
	}

	hostPortDbParts := strings.Split(hostPortDb, "/")
	hostPort := hostPortDbParts[0]
	databaseName := ""
	if len(hostPortDbParts) > 1 {
		databaseName = hostPortDbParts[1]
	}

	hostPortParts := strings.Split(hostPort, ":")
	host := hostPortParts[0]
	port := 5432
	if len(hostPortParts) > 1 {
		fmt.Sscanf(hostPortParts[1], "%d", &port)
	}

	user := "postgres"
	password := ""
	if params != "" {
		paramPairs := strings.Split(params, "&")
		for _, pair := range paramPairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				switch kv[0] {
				case "user":
					user = kv[1]
				case "password":
					password = kv[1]
				}
			}
		}
	}

	return db.Config{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: databaseName,
	}
}
