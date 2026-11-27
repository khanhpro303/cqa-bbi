package main

import (
	"context"
	"log"

	"github.com/vietbui/chat-quality-agent/api"
	"github.com/vietbui/chat-quality-agent/api/handlers"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/cloudifyorders"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pancakelabels"
	"github.com/vietbui/chat-quality-agent/workers"
)

var version = "v1"

func main() {
	log.Printf("Chat Quality Agent %s", version)
	handlers.AppVersion = version

	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize JWT
	middleware.SetJWTSecret(cfg.JWTSecret)

	// Wire the per-sender group-session feature flag into the session key builder
	// before any worker or ERP request runs (write-once, read-only thereafter).
	engine.SetPerSenderGroupSessions(cfg.GroupPerSenderSessions)

	// Connect database
	if err := db.Connect(cfg.DSN(), cfg.IsProduction()); err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	defer db.Close()

	// Connect Redis
	if cfg.RedisURL != "" {
		if err := db.ConnectRedis(cfg.RedisURL); err != nil {
			log.Fatalf("Failed to connect Redis: %v", err)
		}
		defer db.CloseRedis()
	}

	// Run migrations
	if err := db.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	imageRetentionCtx, stopImageRetention := context.WithCancel(context.Background())
	defer stopImageRetention()
	go customercases.RunImageRetention(imageRetentionCtx)

	// Chat memory: record every Zalo OA chatbot turn in MySQL, then summarize
	// idle sessions into customer profiles, issue alerts and a summary index.
	chatmemory.SetEnabled(cfg.ChatMemory.Enabled)
	if cfg.ChatMemory.Enabled {
		zalotoken.SentObserver = chatmemory.RecordOutbound
		memoryCtx, stopMemory := context.WithCancel(context.Background())
		defer stopMemory()
		chatmemory.NewService(cfg).Start(memoryCtx)
	}

	// Chatbot knowledge base: finish indexing that a restart interrupted.
	knowledge.SetEnvDefaults(cfg.ChatMemory.UnstructuredAPIURL, cfg.ChatMemory.UnstructuredAPIKey)
	knowledge.Default().ResumePending()

	importCtx, stopImports := context.WithCancel(context.Background())
	defer stopImports()
	go handlers.RunProductAdvisoryImportWorker(importCtx, cfg)

	// Start scheduler
	scheduler, err := engine.NewScheduler(cfg)
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
	}
	engine.SetDefaultScheduler(scheduler)
	scheduler.Start()
	defer scheduler.Stop()

	// Pancake realtime tags: one headless websocket per tenant (opt-in).
	if cfg.PancakeSocketEnabled {
		listenerCtx, stopListener := context.WithCancel(context.Background())
		defer stopListener()
		go pancakelabels.NewListener(db.DB, cfg.EncryptionKey, cfg.PancakeSocketURL).Run(listenerCtx)
		log.Println("[pancake-socket] realtime tag listener enabled")
	}

	// Cloudify order watcher: detects new/changed ERP orders and messages the
	// customer's Zalo group. Each tenant opts in from Settings.
	if cfg.CloudifyOrderWatchEnabled {
		watcherCtx, stopWatcher := context.WithCancel(context.Background())
		defer stopWatcher()
		go cloudifyorders.NewWatcher(db.DB, cfg.EncryptionKey).Run(watcherCtx)
		log.Println("[cloudify-orders] order watcher enabled")
	}

	// Start worker pool (Asynq server)
	workerPool := workers.NewWorkerPool(cfg, handlers.LookupCurrentZaloOrderDelivery)
	if workerPool != nil {
		go func() {
			if err := workerPool.Start(); err != nil {
				log.Printf("Worker pool error: %v", err)
			}
		}()
		defer workerPool.Stop()
	}

	// Setup router
	router := api.SetupRouter(cfg)

	// Start server
	log.Printf("CQA server starting on %s (env: %s)", cfg.ListenAddr(), cfg.Env)
	if err := router.Run(cfg.ListenAddr()); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
