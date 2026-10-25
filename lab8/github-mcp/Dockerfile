FROM ghcr.io/github/github-mcp-server:latest AS github
FROM supercorp/supergateway:latest
COPY --from=github /server/github-mcp-server /usr/local/bin/github-mcp-server
