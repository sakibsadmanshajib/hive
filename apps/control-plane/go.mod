module github.com/hivegpt/hive/apps/control-plane

go 1.24.0

toolchain go1.24.13

require (
	github.com/google/uuid v1.6.0
	github.com/hibiken/asynq v0.26.0
	github.com/hivegpt/hive/packages/storage v0.0.0
	github.com/jackc/pgx/v5 v5.7.2
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/go-redis/v9 v9.14.1
	github.com/stripe/stripe-go/v84 v84.4.1
)

require (
	github.com/aws/aws-sdk-go-v2 v1.41.5 // indirect
	github.com/aws/smithy-go v1.24.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jung-kurt/gofpdf v1.16.2
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.37.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

replace github.com/hivegpt/hive/packages/storage => ../../packages/storage
