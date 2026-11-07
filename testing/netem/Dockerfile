# The isolated Linux environment of testing/netem/run.sh: tc (iproute2) for netem on lo. The container runs
# with --network none, so lo is its only interface and the tests' tc changes stay inside it.
ARG ALPINE=alpine:3.23
FROM ${ALPINE}
ARG MIRROR=
RUN if [ -n "$MIRROR" ]; then sed -i "s#dl-cdn.alpinelinux.org#$MIRROR#" /etc/apk/repositories; fi \
 && apk add --no-cache iproute2
