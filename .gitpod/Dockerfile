FROM golang:1.12.5

RUN apt-get update && \
  apt-get -y upgrade && \
  apt-get install -y \
    curl \
    git \
    jq \
    openssh-client \
    python \
    python-yaml \
    ruby \
    zip \
    unzip && \
  apt-get clean && \
  rm -rf /var/lib/apt/lists/*
ENV GOPATH=/workspace/go \
    PATH=/workspace/go/bin:$PATH