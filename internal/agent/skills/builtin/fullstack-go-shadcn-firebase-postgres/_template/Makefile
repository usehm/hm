# Common tasks for __APP__.
include .env
export

.PHONY: db backend admin build test sqlc

db: ## start Postgres
	docker compose up -d --wait postgres

backend: ## run the API on :8080
	cd backend && go run ./cmd/server

admin: ## run the admin UI on :5173
	cd admin && npm install && npm run dev

build: ## build everything
	cd backend && go build ./... && go vet ./...
	cd admin && npm install && npm run build

test:
	cd backend && go test ./...

sqlc: ## regenerate the query code
	cd backend && go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate
