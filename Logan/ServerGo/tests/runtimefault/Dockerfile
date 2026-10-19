# Build binary externally with GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test
# -tags runtimefault -c -o runtimefault.test . and use its directory as context.
FROM scratch
ENV TMPDIR=/
COPY runtimefault.test /runtimefault.test
ENTRYPOINT ["/runtimefault.test", "-test.run=^TestRuntimeFaultContainerEntry$", "-test.v", "-test.timeout=100s"]
