
.PHONY: migrate test run tidy vet
DATABASE_PATH ?= data/app.sqlite3
migrate:
	DATABASE_PATH="$(DATABASE_PATH)" go run ./cmd/migrate
test:
	go test ./...
vet:
	go vet ./...
tidy:
	go mod tidy
run:
	DATABASE_PATH="$(DATABASE_PATH)" go run ./cmd/server
