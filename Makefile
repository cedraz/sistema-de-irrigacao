IMAGEM     := icarocedraz/sistema-de-irrigacao
TAG        ?= $(shell date +%Y%m%d-%H%M)
# O servidor é Ubuntu x86_64 e o Mac é ARM. Sem isto a imagem não roda lá.
PLATAFORMA := linux/amd64

.PHONY: help up down logs mongo check publicar

help: ## mostra esta lista
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

up: ## sobe mongo + api + front na sua máquina
	docker compose up -d --build

down: ## para tudo (os dados do Mongo ficam)
	docker compose down

logs: ## acompanha os logs
	docker compose logs -f api front

mongo: ## abre o mongosh
	docker compose exec mongo mongosh irrigacao

check: ## formata, analisa e testa o Go
	cd api && go fmt ./... && go vet ./... && go test ./...

publicar: check ## sobe api e front pro Docker Hub (make publicar TAG=v1)
	docker buildx build --platform $(PLATAFORMA) \
		-t $(IMAGEM):api-$(TAG) -t $(IMAGEM):api-latest --push ./api
	docker buildx build --platform $(PLATAFORMA) \
		-t $(IMAGEM):front-$(TAG) -t $(IMAGEM):front-latest --push ./web
