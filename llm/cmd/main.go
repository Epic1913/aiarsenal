package main

import (
	"context"
	"log"
	"log/slog"

	"github.com/gin-gonic/gin"

	"aiarsenal/llm/logx"
	"aiarsenal/llm/internal/auth"
	"aiarsenal/llm/internal/health"
	"aiarsenal/llm/internal/config"
	"aiarsenal/llm/internal/db"
	"aiarsenal/llm/internal/middleware"
	"aiarsenal/llm/internal/provider"
	"aiarsenal/llm/internal/proxy"
	"aiarsenal/llm/internal/routing"
	"aiarsenal/llm/internal/store"
)

func main() {
	logx.InitEnv("/var/log/aiarsenal/llm", "llm", slog.LevelInfo)

	cfg := config.Load()

	gdb, err := db.InitDB(cfg)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}
	rdb, err := db.InitRedis(cfg)
	if err != nil {
		log.Fatalf("init redis: %v", err)
	}
	// aiarsenal 库只读连接（预算适用范围判定的身份解析）。失败不阻断启动：
	// 圈人预算判定届时 fail-open 放行（与共享预算缺失放行同语义）。
	if _, err := db.InitIAMDB(cfg); err != nil {
		log.Printf("init iam db: %v（预算圈人判定将放行）", err)
	}

	st := store.New(gdb, cfg)
	reg := provider.NewRegistry()
	reg.Register(provider.OpenAIProvider{})
	reg.Register(provider.AnthropicProvider{})
	reg.Register(provider.GeminiProvider{})
	strat := routing.NewWeighted(rdb)
	h := proxy.New(st, reg, strat)

	// 后台部署健康探测：结果落 aa_llm_health_check 并联动路由（连续失败跳过、恢复解除）。
	go health.New(st, reg, strat).Run(context.Background())

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS(cfg.Origin()))

	v1 := r.Group("/v1", auth.AuthMiddleware(st, rdb))
	v1.POST("/chat/completions", h.ChatCompletions)
	v1.POST("/messages", h.Messages)
	v1.POST("/embeddings", h.Embeddings)
	v1.GET("/models", h.Models)

	// 健康检查：暴露 env / domain，便于确认当前环境
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "env": cfg.Env, "domain": cfg.Domain, "origin": cfg.Origin()})
	})

	addr := ":" + cfg.Server.Port
	log.Printf("AI Arsenal LLM router listening on %s (env=%s domain=%s)", addr, cfg.Env, cfg.Domain)
	if err := r.Run(addr); err != nil {
		log.Fatalf("run: %v", err)
	}
}
