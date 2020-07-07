FROM golang

COPY . /usr/src/mutating-admission-controller

WORKDIR /usr/src/mutating-admission-controller

RUN make MOD=vendor install-slim

ENTRYPOINT ["./mutating"]
