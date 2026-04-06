FROM python:3.14-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir trafilatura yt-dlp

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/agent-archiver /usr/bin/agent-archiver
ENTRYPOINT ["/usr/bin/agent-archiver"]
