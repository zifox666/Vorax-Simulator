package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"vorax/internal/application"
	"vorax/internal/storage"
	"vorax/internal/transport"
	"vorax/web"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	rules, err := storage.LoadContent(ctx, os.Getenv("DATABASE_URL"))
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	var signer *application.Signer
	if raw := os.Getenv("SIGNING_KEY_BASE64"); raw != "" {
		key, e := base64.StdEncoding.DecodeString(raw)
		if e != nil {
			log.Fatal("SIGNING_KEY_BASE64 必须是 Base64 编码")
		}
		id := env("SIGNING_KEY_ID", "v1")
		signer, err = application.NewSigner(id, key)
	} else {
		signer, err = application.LoadLocalSigner(env("SIGNING_KEY_FILE", ".local/signing.key"))
	}
	if err != nil {
		log.Fatal(err)
	}
	if raw := os.Getenv("PREVIOUS_SIGNING_KEYS_JSON"); raw != "" {
		var previous map[string]string
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			log.Fatal("旧密钥配置格式错误")
		}
		for id, encoded := range previous {
			key, e := base64.StdEncoding.DecodeString(encoded)
			if e != nil || len(key) < 32 || id == signer.Active {
				log.Fatal("旧密钥配置无效")
			}
			signer.Keys[id] = key
		}
	}
	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), 2*time.Second)
	cache, err := storage.OpenCache(cacheCtx, os.Getenv("REDIS_URL"))
	cacheCancel()
	if err != nil {
		log.Print("Redis 不可用，继续使用无缓存模式")
	}
	if cache != nil {
		defer cache.Client.Close()
	}
	svc := &application.Service{Rules: rules, Signer: signer}
	server := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: transport.Router(svc, cache, http.FS(web.Files)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("渴瘾模拟器 http://%s · %s / %s", server.Addr, rules.Version, rules.ContentVersion)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
