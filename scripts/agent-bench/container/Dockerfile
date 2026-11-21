# The limited agent's computer: Debian with bun (to serve sigma's tools) and git.
FROM oven/bun:1.4.4
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates procps && apt-get clean
WORKDIR /root
ENV HOME=/root
