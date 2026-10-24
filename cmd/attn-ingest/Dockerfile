FROM gcr.io/distroless/static-debian12:nonroot
COPY attn-ingest /attn-ingest
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/attn-ingest"]
