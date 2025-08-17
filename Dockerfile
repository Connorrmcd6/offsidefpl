FROM alpine:3.19
WORKDIR /app
COPY bin/app.exe .
COPY bin/public ./public
COPY bin/.env .
EXPOSE 8090
CMD ["./app.exe", "serve", "--http=0.0.0.0:8090"]