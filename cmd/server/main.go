package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/apikey"
	"github.com/SchemaBio/Sepiida/internal/common/db"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
	"github.com/SchemaBio/Sepiida/internal/server/handler"
	"github.com/SchemaBio/Sepiida/internal/server/middleware"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

func main() {
	// Command line flags
	port := flag.String("p", "9090", "server listen port")
	database := flag.String("d", defaultDatabaseURL(), "database connection string; env: DATABASE_URL")
	// Key files default from env so containers can supply them via env vars
	// instead of being forced to override the entrypoint with explicit flags.
	// Precedence: CLI flag > env var > "" (which we still validate as required).
	agentKeyFile := flag.String("agent-key", firstNonEmptyEnv("SEPIIDA_AGENT_KEY_FILE", "SEPIIDA_AGENT_KEYS_FILE"), "path to agent key file (keys for pushing data); env: SEPIIDA_AGENT_KEY_FILE")
	queryKeyFile := flag.String("query-key", firstNonEmptyEnv("SEPIIDA_QUERY_KEY_FILE", "SEPIIDA_QUERY_KEYS_FILE"), "path to query key file (keys for querying results); env: SEPIIDA_QUERY_KEY_FILE")
	keyRefresh := flag.Int("key-refresh", parsePositiveIntEnv("SEPIIDA_KEY_REFRESH_SECONDS", 30), "key file refresh interval in seconds; env: SEPIIDA_KEY_REFRESH_SECONDS")
	taskTokenSecret := flag.String("task-token-secret", os.Getenv("SEPIIDA_TASK_TOKEN_SECRET"), "shared secret for per-task agent tokens")
	allowStaticAgentKey := flag.Bool("allow-static-agent-key", parseBoolEnv("SEPIIDA_ALLOW_STATIC_AGENT_KEY", false), "allow static agent keys for write APIs (development/compatibility only)")
	flag.Parse()

	// Validate key files. Static agent keys are only needed when explicitly
	// enabling legacy/development writes; production CVM agents should use
	// per-task tokens signed by Squid instead.
	if *agentKeyFile == "" && *allowStaticAgentKey {
		log.Fatal("Error: -agent-key (or SEPIIDA_AGENT_KEY_FILE env) is required when -allow-static-agent-key=true.")
	}
	if *queryKeyFile == "" {
		log.Fatal("Error: -query-key (or SEPIIDA_QUERY_KEY_FILE env) is required. Please specify a query key file path.")
	}
	if *taskTokenSecret == "" && !*allowStaticAgentKey {
		log.Fatal("Error: -task-token-secret is required unless -allow-static-agent-key=true is set for development/compatibility.")
	}
	if *taskTokenSecret != "" && len(*taskTokenSecret) < tasktoken.MinSecretBytes {
		log.Fatalf("Error: -task-token-secret must be at least %d characters. Leave it empty only when -allow-static-agent-key=true is set for development/compatibility.", tasktoken.MinSecretBytes)
	}
	if *keyRefresh <= 0 {
		log.Fatal("Error: -key-refresh must be greater than 0 seconds.")
	}

	// Initialize multi key manager
	mkm := apikey.NewMultiKeyManager(*agentKeyFile, *queryKeyFile)
	mkm.Start(time.Duration(*keyRefresh) * time.Second)

	// Wait for initial key load
	time.Sleep(100 * time.Millisecond)

	agentKeyCount := mkm.AgentKeyCount()
	queryKeyCount := mkm.QueryKeyCount()

	if agentKeyCount == 0 {
		if *allowStaticAgentKey {
			log.Printf("Warning: No agent keys loaded from %s", *agentKeyFile)
		} else {
			log.Printf("Static agent key writes are disabled; no agent keys are required")
		}
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
	agentAuth := middleware.NewAgentAuthMiddleware(mkm.GetAgentKeyManager(), *taskTokenSecret, *allowStaticAgentKey)
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
		if scope, ok := middleware.QueryKeyScope(r.Context()); ok && scope.Restricted() {
			http.Error(w, "query key scope does not allow key management", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v1/keys/status":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "agent_keys_count: %d\nquery_keys_count: %d\nrefresh_interval: %ds\n",
				mkm.AgentKeyCount(), mkm.QueryKeyCount(), *keyRefresh)

		case "/api/v1/keys/reload":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var err error
			if *allowStaticAgentKey {
				err = mkm.ReloadAll()
			} else {
				err = mkm.GetQueryKeyManager().Reload()
			}
			if err != nil {
				log.Printf("Failed to reload key files: %v", err)
				http.Error(w, "failed to reload key files", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Reloaded keys.\nAgent keys: %d\nQuery keys: %d\n",
				mkm.AgentKeyCount(), mkm.QueryKeyCount())

		default:
			http.NotFound(w, r)
		}
	}
	router.Handle("/api/v1/keys/", queryAuth.Middleware(http.HandlerFunc(keysHandler)))

	// Health check endpoint (no authentication required)
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			w.Write([]byte("OK"))
		}
	})

	// Start server
	listenAddr := ":" + *port
	log.Printf("Starting Sepiida Server on %s", listenAddr)
	log.Printf("Database: %s", redactDatabaseURL(*database))
	log.Printf("Agent Key File: %s (%d keys)", *agentKeyFile, agentKeyCount)
	log.Printf("Query Key File: %s (%d keys)", *queryKeyFile, queryKeyCount)
	log.Printf("Static Agent Key Writes: %t", *allowStaticAgentKey)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           securityHeaders(router),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func parseBoolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		log.Printf("Warning: invalid %s=%q, using %t", name, raw, fallback)
		return fallback
	}
	return value
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

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func defaultDatabaseURL() string {
	if value := firstNonEmptyEnv("DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://localhost:5432/sepiida?user=postgres&password=postgres"
}

func redactDatabaseURL(conn string) string {
	u, err := url.Parse(conn)
	if err != nil || u.Scheme == "" {
		return "<redacted>"
	}
	if u.User != nil {
		username := u.User.Username()
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(username, "xxxxx")
		}
	}
	query := u.Query()
	for _, key := range []string{"password", "pass", "pwd"} {
		if query.Has(key) {
			query.Set(key, "xxxxx")
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func parsePostgresConn(conn string) db.Config {
	if strings.Contains(conn, "://") {
		return db.Config{DSN: conn}
	}

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
