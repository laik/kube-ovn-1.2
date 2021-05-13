GOFILES_NOVENDOR=$(shell find . -type f -name '*.go' -not -path "./vendor/*")
GO_VERSION=1.14

REGISTRY=harbor.ym/devops
DEV_TAG=dev
RELEASE_TAG=$(shell cat VERSION)

# ARCH could be amd64,arm64
ARCH=amd64
# RPM_ARCH could be x86_64,aarch64
RPM_ARCH=x86_64

.PHONY: build-dev-images build-go build-bin lint kind-init kind-init-ha kind-reload push-dev push-release e2e ut

build-dev-images: build-bin
	docker build -t ${REGISTRY}/kube-ovn:${DEV_TAG} -f dist/images/Dockerfile dist/images/
	
image:
	docker build -t ${REGISTRY}/kube-ovn:${DEV_TAG} -f dist/images/Dockerfile dist/images/
push-dev:
	docker push ${REGISTRY}/kube-ovn:${DEV_TAG}

build-go:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(PWD)/dist/images/kube-ovn -ldflags "-w -s" -v ./cmd/cni
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(PWD)/dist/images/kube-ovn-controller -ldflags "-w -s" -v ./cmd/controller
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(PWD)/dist/images/kube-ovn-daemon -ldflags "-w -s" -v ./cmd/daemon
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(PWD)/dist/images/kube-ovn-pinger -ldflags "-w -s" -v ./cmd/pinger
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(PWD)/dist/images/kube-ovn-webhook -ldflags "-w -s" -v ./cmd/webhook
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(PWD)/dist/images/kube-ovn-speaker -ldflags "-w -s" -v ./cmd/speaker

build-go-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(PWD)/dist/images/kube-ovn -ldflags "-w -s" -v ./cmd/cni
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(PWD)/dist/images/kube-ovn-controller -ldflags "-w -s" -v ./cmd/controller
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(PWD)/dist/images/kube-ovn-daemon -ldflags "-w -s" -v ./cmd/daemon
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(PWD)/dist/images/kube-ovn-pinger -ldflags "-w -s" -v ./cmd/pinger
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(PWD)/dist/images/kube-ovn-webhook -ldflags "-w -s" -v ./cmd/webhook
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(PWD)/dist/images/kube-ovn-speaker -ldflags "-w -s" -v ./cmd/speaker

release: lint build-go
	docker buildx build --platform linux/amd64 --build-arg ARCH=amd64 --build-arg RPM_ARCH=x86_64 -t ${REGISTRY}/kube-ovn:${RELEASE_TAG} -o type=docker -f dist/images/Dockerfile dist/images/

release-arm: lint build-go-arm
	docker buildx build --platform linux/arm64 --build-arg ARCH=arm64 --build-arg RPM_ARCH=aarch64 -t ${REGISTRY}/kube-ovn:${RELEASE_TAG} -o type=docker -f dist/images/Dockerfile dist/images/

push-release: release
	docker push ${REGISTRY}/kube-ovn:${RELEASE_TAG}

lint:
	@gofmt -d ${GOFILES_NOVENDOR} 
	@gofmt -l ${GOFILES_NOVENDOR} | read && echo "Code differs from gofmt's style" 1>&2 && exit 1 || true
	@GOOS=linux go vet ./...

build-bin:
	docker run --rm -e GOOS=linux -e GOCACHE=/tmp -e GOARCH=${ARCH} -e GOPROXY=https://goproxy.cn \
		-u $(shell id -u):$(shell id -g) \
		-v $(CURDIR):/go/src/github.com/alauda/kube-ovn:ro \
		-v $(CURDIR)/dist:/go/src/github.com/alauda/kube-ovn/dist/ \
		golang:$(GO_VERSION) /bin/bash -c '\
		cd /go/src/github.com/alauda/kube-ovn && \
		make build-go '

kind-init:
	kind delete cluster --name=kube-ovn
	kind create cluster --config yamls/kind-single.yaml --name kube-ovn
	kind load docker-image --name kube-ovn ${REGISTRY}/kube-ovn:${RELEASE_TAG}
	bash dist/images/install.sh

kind-init-ha:
	kind delete cluster --name=kube-ovn
	kind create cluster --config yamls/kind-ha.yaml --name kube-ovn
	kind load docker-image --name kube-ovn ${REGISTRY}/kube-ovn:${RELEASE_TAG}
	bash dist/images/install.sh

kind-reload:
	kind load docker-image --name kube-ovn ${REGISTRY}/kube-ovn:${RELEASE_TAG}
	kubectl delete pod -n kube-system -l app=kube-ovn-controller

kind-clean:
	kind delete cluster --name=kube-ovn

uninstall:
	bash dist/images/cleanup.sh

e2e:
	ginkgo -p --slowSpecThreshold=60 test/e2e

ut:
	ginkgo -p --slowSpecThreshold=60 test/unittest
