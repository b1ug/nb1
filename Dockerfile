FROM ubuntu:20.04

ARG ROOT_PATH=/root
ARG BINARY=nb1

ENV BINPATH=${ROOT_PATH}/${BINARY}
ENV TERM=xterm-256color
ENV TZ=Asia/Shanghai
ENV PORT=80

EXPOSE 80
WORKDIR ${ROOT_PATH}

COPY ${BINARY} ${ROOT_PATH}/

RUN set -eux; \
    apt-get update \
    && apt-get install -y --no-install-recommends apt-transport-https ca-certificates tzdata \
    && apt-get clean && rm -rf /var/lib/apt/lists/* ; \
    ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo ${TZ} > /etc/timezone ; \
    chmod +x ${BINPATH}

CMD ["sh", "-c", "${BINPATH}"]
