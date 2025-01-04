# Load environment variables from .env file
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

run: build
	@./bin/app serve

build:
	@GOOS=linux GOARCH=arm64 go build -o bin/app .

dev:
	@make air & make css & make templ

air:
	@air

css:
	@tailwindcss -i app/css/app.css -o public/styles.css --watch   

templ:
	@templ generate --watch --proxy="http://localhost$(LISTEN_ADDR)" --open-browser=true
