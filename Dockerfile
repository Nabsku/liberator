FROM golang:1.24.2-alpine3.21

COPY liberator /liberator

ENTRYPOINT ["/liberator"]
