package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/db"
	"github.com/SchemaBio/Sepiida/internal/server/handler"
	"github.com/SchemaBio/Sepiida/internal/server/middleware"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

func main() {
	// Command line flags
	port := flag.String("p", "8080", "server listen port")
	database := flag.String("d", "sqlite://data/sepiida.db", "database connection string (sqlite://path or postgres://host:port/db?user=xxx&password=xxx)")
	apiKeys := flag.String("key", "", "API keys (comma separated)")
	flag.Parse()

	// Parse API keys
	keys := parseAPIKeys(*apiKeys)
	if len(keys) == 0 {
		keys = []string{"default-api-key"}
		log.Println("Warning: using default API key. Please specify -key for production use.")
	}

	// Parse database connection
	dbType, dbConn := parseDatabaseConn(*database)

	// Initialize database
	databaseObj, err := initDatabase(dbType, dbConn)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer databaseObj.Close()

	// Initialize database tables
	if err := databaseObj.Initialize(context.Background()); err != nil {
		log.Fatalf("Failed to initialize database tables: %v", err)
	}

	// Create initial API keys
	for _, key := range keys {
		if err := databaseObj.CreateAPIKey(context.Background(), key, "cli-provided"); err != nil {
			log.Printf("Note: API key may already exist: %v", err)
		}
	}

	// Create service and handler
	workflowService := service.NewWorkflowService(databaseObj)
	progressHandler := handler.NewProgressHandler(workflowService)

	// Create authentication middleware
	authMiddleware := middleware.NewAuthMiddleware(databaseObj.ValidateAPIKey)

	// Setup routes
	router := http.NewServeMux()

	// API routes with authentication
	apiHandler := http.NewServeMux()
	apiHandler.HandleFunc("/api/v1/progress", progressHandler.HandleProgress)
	apiHandler.HandleFunc("/api/v1/workflow/output", progressHandler.HandleOutput)
	apiHandler.HandleFunc("/api/v1/workflow", progressHandler.HandleGetWorkflow)
	apiHandler.HandleFunc("/api/v1/workflow/tasks", progressHandler.HandleGetWorkflowTasks)
	apiHandler.HandleFunc("/api/v1/workflows", progressHandler.HandleListWorkflows)

	// Apply authentication middleware to API routes
	router.Handle("/api/", authMiddleware.Middleware(apiHandler))

	// Health check endpoint (no authentication required)
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Start server
	listenAddr := ":" + *port
	log.Printf("Starting Sepiida Server on %s", listenAddr)
	log.Printf("Database: %s", *database)
	log.Printf("API Keys: %d keys configured", len(keys))

	if err := http.ListenAndServe(listenAddr, router); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func parseAPIKeys(keyStr string) []string {
	if keyStr == "" {
		return nil
	}
	return strings.Split(keyStr, ",")
}

func parseDatabaseConn(conn string) (string, string) {
	// Format: sqlite://path or postgres://host:port/db?user=xxx&password=xxx
	if strings.HasPrefix(conn, "sqlite://") {
		return "sqlite", strings.TrimPrefix(conn, "sqlite://")
	}
	if strings.HasPrefix(conn, "postgres://") {
		return "postgres", strings.TrimPrefix(conn, "postgres://")
	}
	// Default to sqlite with the path
	return "sqlite", conn
}

func initDatabase(dbType, dbConn string) (db.Database, error) {
	switch dbType {
	case "sqlite":
		// Ensure data directory exists
		dir := strings.TrimSuffix(dbConn, "/"+strings.Split(dbConn, "/")[len(strings.Split(dbConn, "/"))-1])
		if dir != "" && dir != dbConn {
			os.MkdirAll(dir, 0755)
		}
		return db.NewSQLite(dbConn)
	case "postgres":
		// Parse postgres connection: host:port/db?user=xxx&password=xxx
		return parsePostgresConn(dbConn)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
}

func parsePostgresConn(conn string) (db.Database, error) {
	// Simple parsing: host:port/database?user=xxx&password=xxx
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

	cfg := db.Config{
		Type:            "postgres",
		PostgresHost:     host,
		PostgresPort:     port,
		PostgresUser:     user,
		PostgresPassword: password,
		PostgresDatabase: databaseName,
	}

	return db.NewPostgreSQL(cfg)
}