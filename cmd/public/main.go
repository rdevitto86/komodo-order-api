package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"komodo-order-api/internal/api"
	"komodo-order-api/internal/config"
	"komodo-order-api/internal/repo"

	"github.com/rdevitto86/komodo-forge-sdk-go/api/handlers/health"

	"github.com/rdevitto86/komodo-forge-sdk-go/aws/dynamodb"
	awsSM "github.com/rdevitto86/komodo-forge-sdk-go/aws/secretsmanager"
	"github.com/rdevitto86/komodo-forge-sdk-go/db/redis"
	"github.com/rdevitto86/komodo-forge-sdk-go/crypto/jwt"
	mw "github.com/rdevitto86/komodo-forge-sdk-go/api/middleware"
	srv "github.com/rdevitto86/komodo-forge-sdk-go/api/server"
	logger "github.com/rdevitto86/komodo-forge-sdk-go/logging/runtime"
)

// init runs once per execution environment (cold start on Lambda, once on Fargate/local).
// AWS client bootstrapping lives here so warm Lambda invocations skip it entirely.
func init() {
	logger.Init(
		os.Getenv(config.APP_NAME),
		os.Getenv(config.LOG_LEVEL),
		os.Getenv(config.ENV),
	)

	smCfg := awsSM.Config{
		Region:   os.Getenv(config.AWS_REGION),
		Endpoint: os.Getenv(config.AWS_ENDPOINT),
		Prefix:   os.Getenv(config.AWS_SECRET_PREFIX),
		Batch:    os.Getenv(config.AWS_SECRET_BATCH),
		Keys: []string{
			config.AWS_ELASTICACHE_ENDPOINT,
			config.AWS_ELASTICACHE_PASSWORD,
			config.AWS_ELASTICACHE_DB,
			config.DYNAMODB_ORDERS_TABLE,
			config.DYNAMODB_ACCESS_KEY,
			config.DYNAMODB_SECRET_KEY,
			config.DYNAMODB_ENDPOINT,
			config.CART_API_URL,
			config.INVENTORY_API_URL,
			config.JWT_PUBLIC_KEY,
			config.JWT_PRIVATE_KEY,
			config.JWT_ISSUER,
			config.JWT_AUDIENCE,
			config.JWT_KID,
			config.MAX_CONTENT_LENGTH,
			config.RATE_LIMIT_RPS,
			config.RATE_LIMIT_BURST,
			config.IDEMPOTENCY_TTL_SEC,
			config.IP_WHITELIST,
			config.IP_BLACKLIST,
			config.BUCKET_TTL_SECOND,
		},
	}
	sm, err := awsSM.New(context.Background(), smCfg)
	if err != nil {
		logger.Fatal("failed to initialize secrets manager", err)
		os.Exit(1)
	}
	secrets, err := sm.GetSecrets(smCfg.Keys, smCfg.Prefix, smCfg.Batch)
	if err != nil {
		logger.Fatal("failed to fetch secrets", err)
		os.Exit(1)
	}
	for k, v := range secrets {
		os.Setenv(k, v)
	}

	ddbCfg := dynamodb.Config{
		Region:    os.Getenv(config.AWS_REGION),
		Endpoint:  os.Getenv(config.DYNAMODB_ENDPOINT),
		AccessKey: os.Getenv(config.DYNAMODB_ACCESS_KEY),
		SecretKey: os.Getenv(config.DYNAMODB_SECRET_KEY),
	}
	ddbClient, err := dynamodb.New(context.Background(), ddbCfg)
	if err != nil {
		logger.Fatal("failed to initialize dynamodb", err)
		os.Exit(1)
	}
	repo.DynDB = ddbClient

	ecClient, err := redis.NewFromDBString(
		os.Getenv(config.AWS_ELASTICACHE_ENDPOINT),
		os.Getenv(config.AWS_ELASTICACHE_PASSWORD),
		os.Getenv(config.AWS_ELASTICACHE_DB),
	)
	if err != nil {
		logger.Fatal("failed to initialize elasticache", err)
		os.Exit(1)
	}

	api.EC = ecClient

	// order-api validates incoming user JWTs. Both public and private keys are loaded
	// so the service can verify tokens issued by auth-api.
	if err := jwt.InitializeKeys(); err != nil {
		logger.Fatal("failed to initialize JWT keys", err)
		os.Exit(1)
	}

	logger.Info("order-api public: bootstrap complete")
}

func main() {
	// order-api has no real adapters yet — pass nil so the service skips
	// cart-api token validation and inventory hold confirmation until the
	// HTTP adapter implementations land.
	//
	// TODO: wire real adapters once cart-api and shop-inventory-api HTTP clients
	// are implemented under internal/adapters/.
	// nil adapters: cart, inventory, and user service adapters are not yet wired.
	// TODO: wire real adapters once HTTP clients land under internal/adapters/.
	svc := api.NewService(api.NewOrderService(nil, nil, nil, nil))

	writeMW := []func(http.Handler) http.Handler{
		mw.RequestIDMiddleware,
		mw.TelemetryMiddleware,
		mw.RateLimiterMiddleware,
		mw.CORSMiddleware,
		mw.SecurityHeadersMiddleware,
		mw.AuthMiddleware,
		mw.CSRFMiddleware,
		mw.NormalizationMiddleware,
		mw.RuleValidationMiddleware,
		mw.SanitizationMiddleware,
		mw.IdempotencyMiddleware,
	}

	readMW := []func(http.Handler) http.Handler{
		mw.RequestIDMiddleware,
		mw.TelemetryMiddleware,
		mw.RateLimiterMiddleware,
		mw.CORSMiddleware,
		mw.SecurityHeadersMiddleware,
		mw.AuthMiddleware,
		mw.CSRFMiddleware,
		mw.NormalizationMiddleware,
		mw.RuleValidationMiddleware,
		mw.SanitizationMiddleware,
	}

	// guestWriteMW is the same as writeMW but without AuthMiddleware, allowing
	// unauthenticated (guest) callers through. The handler validates identity
	// via optional JWT context or email in the request body.
	guestWriteMW := []func(http.Handler) http.Handler{
		mw.RequestIDMiddleware,
		mw.TelemetryMiddleware,
		mw.RateLimiterMiddleware,
		mw.CORSMiddleware,
		mw.SecurityHeadersMiddleware,
		mw.CSRFMiddleware,
		mw.NormalizationMiddleware,
		mw.RuleValidationMiddleware,
		mw.SanitizationMiddleware,
		mw.IdempotencyMiddleware,
	}

	// guestReadMW is the read stack without AuthMiddleware — JWT is optional.
	// Used for GET /orders/{orderId} which supports both authenticated and guest access.
	guestReadMW := []func(http.Handler) http.Handler{
		mw.RequestIDMiddleware,
		mw.TelemetryMiddleware,
		mw.RateLimiterMiddleware,
		mw.CORSMiddleware,
		mw.SecurityHeadersMiddleware,
		mw.NormalizationMiddleware,
		mw.RuleValidationMiddleware,
		mw.SanitizationMiddleware,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.HealthHandler)

	// Unified order submission — optional JWT; email required for guests.
	mux.Handle("POST /v1/orders", mw.Chain(http.HandlerFunc(svc.PlaceOrderUnifiedHandler), guestWriteMW...))

	// Guest-compatible unified order lookup — no JWT required.
	// Registered before /v1/me/orders/{orderId} to avoid ServeMux ambiguity.
	mux.Handle("GET /v1/orders/{orderId}", mw.Chain(http.HandlerFunc(svc.GetOrderUnifiedHandler), guestReadMW...))

	// Authenticated order routes — require JWT.
	mux.Handle("GET /v1/me/orders", mw.Chain(http.HandlerFunc(svc.ListOrdersHandler), readMW...))
	mux.Handle("GET /v1/me/orders/{orderId}", mw.Chain(http.HandlerFunc(svc.GetOrderHandler), readMW...))
	mux.Handle("POST /v1/me/orders/{orderId}/cancel", mw.Chain(http.HandlerFunc(svc.CancelOrderHandler), writeMW...))

	// Authenticated returns (RMA) routes — registered before /v1/me/orders/{orderId} to
	// prevent the wildcard pattern from consuming the literal "returns" segment.
	mux.Handle("GET /v1/me/orders/returns", mw.Chain(http.HandlerFunc(svc.ListReturnsHandler), readMW...))
	mux.Handle("POST /v1/me/orders/returns", mw.Chain(http.HandlerFunc(svc.CreateReturnHandler), writeMW...))
	mux.Handle("GET /v1/me/orders/returns/{returnId}", mw.Chain(http.HandlerFunc(svc.GetReturnHandler), readMW...))
	mux.Handle("DELETE /v1/me/orders/returns/{returnId}", mw.Chain(http.HandlerFunc(svc.CancelReturnHandler), writeMW...))

	server := &http.Server{
		Handler:           mux,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	srv.Run(server, os.Getenv(config.PORT), 30*time.Second)
}

// mustParseInt64 parses s as int64. Returns fallback on empty or parse failure.
func mustParseInt64(s string, fallback int64) int64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}
