.PHONY: dev build test test-race smoke compose-up compose-down demo

dev:
	npm run dev

build:
	npm run build
	docker build --target server -t monitored/server:dev .
	docker build --target agent -t monitored/agent:dev .

test:
	go test ./...
	CGO_ENABLED=0 go test ./...
	go vet ./...
	npm test
	npm run audit:prod
	npm run lint
	npx tsc --noEmit
	docker build --target test -t monitored/test:dev .

test-race:
	go test -race ./...

smoke:
	./scripts/smoke-compose.sh

compose-up:
	docker compose up --build

compose-down:
	docker compose down

demo:
	docker compose -f compose.demo.yaml up --build
