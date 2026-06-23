# Linux acceptance runner: builds ESFS and runs the parity loop against a real
# Elasticsearch (provided by docker-compose). Includes fuse3 so the kernel mount
# (Tier B) can be exercised when the container has /dev/fuse + SYS_ADMIN.
FROM golang:1.26-bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends fuse3 curl jq ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# Allow non-root FUSE if ever needed; harmless when running as root.
RUN sed -i 's/#user_allow_other/user_allow_other/' /etc/fuse.conf || true

WORKDIR /src
COPY . .
RUN make build

ENV PATH="/src/bin:${PATH}"
ENTRYPOINT ["/src/test/docker/acceptance.sh"]
