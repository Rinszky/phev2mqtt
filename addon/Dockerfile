ARG BUILD_FROM
FROM $BUILD_FROM AS builder

ARG REPO_URL=https://github.com/buxtronix/phev2mqtt.git
ARG REPO_BRANCH=master

RUN apk update && apk add --no-cache \
	g++ \
	gcc \
	git \
	libpcap-dev \
	musl-dev

COPY --from=golang:1.21-alpine /usr/local/go/ /usr/local/go/
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /tmp/buildctx
COPY . /tmp/buildctx/

RUN if [ -f /tmp/buildctx/go.mod ]; then \
		cp -r /tmp/buildctx /tmp/phev2mqtt; \
	else \
		git clone --branch "${REPO_BRANCH}" "${REPO_URL}" /tmp/phev2mqtt; \
	fi && \
	cd /tmp/phev2mqtt && \
	go build -o /tmp/phev2mqtt/phev2mqtt .

FROM $BUILD_FROM
RUN apk update && apk add --no-cache \
	iproute2 \
	libpcap \
	tzdata

COPY --from=builder /tmp/phev2mqtt/phev2mqtt /opt/phev2mqtt
COPY run.sh /opt/run.sh
RUN chmod a+x /opt/run.sh

CMD [ "/opt/run.sh" ]
