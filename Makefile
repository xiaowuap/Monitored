.PHONY: dev build test compose-up compose-down

dev:
	npm run dev

build:
	npm run build
	docker build --target server -t monitored/server:dev .
	docker build --target agent -t monitored/agent:dev .

test:
	npm test
	docker build --target test -t monitored/test:dev .

compose-up:
	docker compose up --build

compose-down:
	docker compose down
