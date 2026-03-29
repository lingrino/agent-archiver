FROM scratch
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/agent-archiver /usr/bin/agent-archiver
ENTRYPOINT ["/usr/bin/agent-archiver"]
