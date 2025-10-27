FROM golang:1.25-alpine AS build

WORKDIR /app
COPY . .

ENV CGO_ENABLED=0
RUN go build -o solar-agent .

###
FROM scratch

COPY --from=build /app/solar-agent /solar-agent
VOLUME /config

CMD ["/solar-agent", "agent", "-config", "/config/config.yaml"]
