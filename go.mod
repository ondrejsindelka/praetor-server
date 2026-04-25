module github.com/ondrejsindelka/praetor-server

go 1.25.7

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/oklog/ulid/v2 v2.1.1
	github.com/ondrejsindelka/praetor-proto v0.1.0
	github.com/pressly/goose/v3 v3.27.1
	google.golang.org/grpc v1.80.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/ondrejsindelka/praetor-proto => ../praetor-proto

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/rogpeppe/go-internal v1.6.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260420184626-e10c466a9529 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
