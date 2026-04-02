FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /syncer .

# Minimal runtime image (~10MB)
FROM alpine:3.19

ARG USERNAME=hluser
ARG USER_UID=10000
ARG USER_GID=$USER_UID

RUN addgroup -g $USER_GID $USERNAME \
    && adduser -D -u $USER_UID -G $USERNAME $USERNAME \
    && mkdir -p /home/$USERNAME/hl/data && chown -R $USERNAME:$USERNAME /home/$USERNAME/hl

COPY --from=builder /syncer /usr/local/bin/syncer

USER $USERNAME
ENTRYPOINT ["syncer"]
