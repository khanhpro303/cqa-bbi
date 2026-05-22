package workers

import (
	"context"
	"log"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

type WorkerPool struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	cfg    *config.Config
}

func NewWorkerPool(cfg *config.Config) *WorkerPool {
	if cfg.RedisURL == "" {
		log.Println("[worker] REDIS_URL is not set, task processing will be disabled")
		return nil
	}

	opt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.Printf("[worker] failed to parse Redis URL: %v", err)
		return nil
	}

	srv := asynq.NewServer(
		opt,
		asynq.Config{
			// Tối đa 10 luồng xử lý đồng thời để bảo vệ Langflow API
			Concurrency: 10,
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      1,
			},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				log.Printf("[worker] task %q failed: %v", task.Type(), err)
			}),
		},
	)

	mux := asynq.NewServeMux()

	langflowClient := engine.NewLangflowClient(cfg)
	
	// Register handlers
	mux.HandleFunc(TypeZaloWebhook, HandleZaloWebhookTask(cfg, langflowClient))

	return &WorkerPool{
		server: srv,
		mux:    mux,
		cfg:    cfg,
	}
}

func (wp *WorkerPool) Start() error {
	if wp == nil || wp.server == nil {
		return nil
	}
	log.Println("[worker] starting asynq server...")
	return wp.server.Start(wp.mux)
}

func (wp *WorkerPool) Stop() {
	if wp == nil || wp.server == nil {
		return
	}
	log.Println("[worker] stopping asynq server...")
	wp.server.Stop()
}
