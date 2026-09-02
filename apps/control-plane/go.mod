module github.com/sakibsadmanshajib/hive/apps/control-plane

go 1.26.0

toolchain go1.26.7

require (
	github.com/google/uuid v1.6.0
	github.com/hibiken/asynq v0.26.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/prometheus/client_golang v1.24.1
	github.com/redis/go-redis/v9 v9.22.0
	github.com/sakibsadmanshajib/hive/apps/agent-engine v0.0.0
	github.com/sakibsadmanshajib/hive/packages/audit-canonical v0.0.0
	github.com/sakibsadmanshajib/hive/packages/budgetkeys v0.0.0
	github.com/sakibsadmanshajib/hive/packages/dbtest v0.0.0
	github.com/sakibsadmanshajib/hive/packages/embedmodel v0.0.0
	github.com/sakibsadmanshajib/hive/packages/sanitize v0.0.0
	github.com/sakibsadmanshajib/hive/packages/storage v0.0.0
	github.com/stripe/stripe-go/v84 v84.4.1
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)

require (
	github.com/stretchr/testify v1.12.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/aws/aws-sdk-go-v2 v1.41.5 // indirect
	github.com/aws/smithy-go v1.24.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jung-kurt/gofpdf v1.16.2
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/sakibsadmanshajib/hive/packages/storage => ../../packages/storage

replace github.com/sakibsadmanshajib/hive/packages/audit-canonical => ../../packages/audit-canonical

replace github.com/sakibsadmanshajib/hive/packages/budgetkeys => ../../packages/budgetkeys

replace github.com/sakibsadmanshajib/hive/packages/dbtest => ../../packages/dbtest

replace github.com/sakibsadmanshajib/hive/packages/embedmodel => ../../packages/embedmodel

replace github.com/sakibsadmanshajib/hive/packages/sanitize => ../../packages/sanitize

replace github.com/sakibsadmanshajib/hive/apps/agent-engine => ../agent-engine
