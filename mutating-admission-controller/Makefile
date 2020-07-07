LDFLAGS := "-extldflags '-static'"

MOD ?= vendor

.PHONY: install-slim
# Once we change CI code to build outside GOPATH, GO111MODULE can be removed,
# so we rely on defaults.
install-slim:
	CGO_ENABLED=0 GO111MODULE=on go build -o=mutating \
		-mod=vendor \
		-ldflags $(LDFLAGS) \

.PHONY: install-webhook
install-webhook:
	install/webhook-run.sh
