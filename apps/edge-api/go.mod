module github.com/sakibsadmanshajib/hive/apps/edge-api

go 1.26.0

toolchain go1.26.7

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6
	github.com/jackc/pgx/v5 v5.10.0
	github.com/lestrrat-go/jwx/v2 v2.1.7
	github.com/prometheus/client_golang v1.24.1
	github.com/prometheus/client_model v0.6.2
	github.com/sakibsadmanshajib/hive/packages/audit-canonical v0.0.0
	github.com/sakibsadmanshajib/hive/packages/budgetkeys v0.0.0
	github.com/sakibsadmanshajib/hive/packages/ratewindows v0.0.0
	github.com/sakibsadmanshajib/hive/packages/embedmodel v0.0.0
	github.com/sakibsadmanshajib/hive/packages/sanitize v0.0.0
	github.com/stretchr/testify v1.12.1
	golang.org/x/sync v0.22.0
)

require (
	github.com/aws/aws-sdk-go-v2 v1.41.5 // indirect
	github.com/aws/smithy-go v1.24.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc v1.0.6 // indirect
	github.com/lestrrat-go/iter v1.0.2 // indirect
	github.com/lestrrat-go/option v1.0.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/sakibsadmanshajib/hive/packages/dbtest v0.0.0
	github.com/sakibsadmanshajib/hive/packages/storage v0.0.0
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/sakibsadmanshajib/hive/packages/storage => ../../packages/storage

replace github.com/sakibsadmanshajib/hive/packages/audit-canonical => ../../packages/audit-canonical

replace github.com/sakibsadmanshajib/hive/packages/budgetkeys => ../../packages/budgetkeys

replace github.com/sakibsadmanshajib/hive/packages/ratewindows => ../../packages/ratewindows

replace github.com/sakibsadmanshajib/hive/packages/dbtest => ../../packages/dbtest

replace github.com/sakibsadmanshajib/hive/packages/embedmodel => ../../packages/embedmodel

replace github.com/sakibsadmanshajib/hive/packages/sanitize => ../../packages/sanitize
