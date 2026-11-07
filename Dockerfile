FROM golang:1.21-alpine AS builder

RUN apk add --no-cache \
	g++ \
	gcc \
	git \
	libpcap-dev \
	musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/phev2mqtt .

FROM alpine:3.19
RUN apk add --no-cache \
	ca-certificates \
	iproute2 \
	libpcap \
	tzdata

COPY --from=builder /out/phev2mqtt /usr/local/bin/phev2mqtt

ENTRYPOINT ["/usr/local/bin/phev2mqtt"]
CMD ["client", "mqtt"]
