# faster-together-club collector

Cross-platform Go binary that turns Forza Horizon 6 telemetry into lap uploads
to an FasterTogetherClub server.

For end-user setup instructions see `../docs/setup.md` (added in Phase 7).

## Build

```sh
cd collector
go build -o bin/collector ./cmd/collector
```

## Run (local dev against `localhost:4001`)

```sh
./bin/collector login --server http://localhost:4001
./bin/collector       --server http://localhost:4001
```

## Tests

```sh
go test ./...
```
