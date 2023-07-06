GOCMD=go
GOBUILD=$(GOCMD) build
GOFMT=$(GOCMD)fmt
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=mysocketctl
BUCKET=pub-mysocketctl-bin

DATE := $(shell git log -1 --format=%cd --date=format:"%Y%m%d")
VERSION=$(BORDER0_VERSION)
FLAGS := -ldflags "-X github.com/borderzero/border0-cli/cmd.version=$(VERSION) -X github.com/borderzero/border0-cli/cmd.date=$(DATE)"

all: lint moddownload test build

release:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_windows_amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_arm
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_armv6
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_darwin_amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_darwin_arm64
	CGO_ENABLED=0 GOOS=openbsd GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_openbsd_amd64

	shasum -a 256 ./bin/mysocketctl_darwin_amd64 | awk '{print $$1}' > ./bin/mysocketctl_darwin_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_darwin_amd64-sha256-checksum.txt ${BUCKET} darwin_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_darwin_amd64 ${BUCKET} darwin_amd64/mysocketctl

	shasum -a 256 ./bin/mysocketctl_darwin_arm64 | awk '{print $$1}' > ./bin/mysocketctl_darwin_arm64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_darwin_arm64-sha256-checksum.txt ${BUCKET} darwin_arm64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_darwin_arm64 ${BUCKET} darwin_arm64/mysocketctl

	shasum -a 256 ./bin/mysocketctl_openbsd_amd64 | awk '{print $$1}' > ./bin/mysocketctl_openbsd_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_openbsd_amd64-sha256-checksum.txt ${BUCKET} openbsd_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_openbsd_amd64 ${BUCKET} openbsd_amd64/mysocketctl

	shasum -a 256 ./bin/mysocketctl_linux_amd64 | awk '{print $$1}' > ./bin/mysocketctl_linux_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_amd64-sha256-checksum.txt ${BUCKET} linux_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_amd64 ${BUCKET} linux_amd64/mysocketctl

	#This is for Raspberrypi
	shasum -a 256 ./bin/mysocketctl_linux_arm64 | awk '{print $$1}' > ./bin/mysocketctl_linux_arm64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_arm64-sha256-checksum.txt ${BUCKET} linux_arm64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_arm64 ${BUCKET} linux_arm64/mysocketctl

	#This is for Raspberrypi 32bit
	shasum -a 256 ./bin/mysocketctl_linux_arm | awk '{print $$1}' > ./bin/mysocketctl_linux_arm-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_arm-sha256-checksum.txt ${BUCKET} linux_arm/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_arm ${BUCKET} linux_arm/mysocketctl

	#This is for Raspberrypi arm v6 32bit
	shasum -a 256 ./bin/mysocketctl_linux_armv6 | awk '{print $$1}' > ./bin/mysocketctl_linux_armv6-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_armv6-sha256-checksum.txt ${BUCKET} linux_armv6/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_linux_armv6 ${BUCKET} linux_armv6/mysocketctl

	shasum -a 256 ./bin/mysocketctl_windows_amd64 | awk '{print $$1}' > ./bin/mysocketctl_windows_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_windows_amd64-sha256-checksum.txt ${BUCKET} windows_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/mysocketctl_windows_amd64 ${BUCKET} windows_amd64/mysocketctl.exe

	echo ${VERSION} > latest_version.txt
	python3 ./s3upload.py latest_version.txt ${BUCKET} latest_version.txt
	rm latest_version.txt

release-border0:
	python3 ./s3upload.py ./bin/mysocketctl_darwin_amd64 ${BUCKET} darwin_amd64/border0
	python3 ./s3upload.py ./bin/mysocketctl_darwin_arm64 ${BUCKET} darwin_arm64/border0
	python3 ./s3upload.py ./bin/mysocketctl_linux_amd64 ${BUCKET} linux_amd64/border0
	python3 ./s3upload.py ./bin/mysocketctl_openbsd_amd64 ${BUCKET} openbsd_amd64/border0

	#This is for Raspberrypi
	python3 ./s3upload.py ./bin/mysocketctl_linux_arm64 ${BUCKET} linux_arm64/border0

	#This is for Raspberrypi 32bit
	python3 ./s3upload.py ./bin/mysocketctl_linux_arm ${BUCKET} linux_arm/border0

	#This is for Windows
	python3 ./s3upload.py ./bin/mysocketctl_windows_amd64 ${BUCKET} windows_amd64/border0.exe

	#This is for Raspberrypi armv6 32bit
	python3 ./s3upload.py ./bin/mysocketctl_linux_armv6 ${BUCKET} linux_armv6/border0

moddownload:
	go mod tidy
	go mod download

build:
	$(GOBUILD) $(FLAGS) -o $(BINARY_NAME) -v
	-rm -f border0
	cp $(BINARY_NAME) border0

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(FLAGS) -o $(BINARY_NAME) -v
	cp $(BINARY_NAME) border0

build-all:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(FLAGS) -o $(BINARY_NAME)_windows_amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(FLAGS) -o $(BINARY_NAME)_linux_amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(FLAGS) -o $(BINARY_NAME)_linux_arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm go build $(FLAGS) -o $(BINARY_NAME)_linux_arm
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build $(FLAGS) -o $(BINARY_NAME)_linux_armv6
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(FLAGS) -o $(BINARY_NAME)_darwin_amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(FLAGS) -o $(BINARY_NAME)_darwin_arm64
	CGO_ENABLED=0 GOOS=openbsd GOARCH=amd64 go build $(FLAGS) -o $(BINARY_NAME)_openbsd_amd64

lint:
	@echo "running go fmt"
	$(GOFMT) -w .

test:
	$(GOTEST) -cover ./...

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

run:
	$(GOBUILD) $(FLAGS) -o $(BINARY_NAME) -v ./...
	./$(BINARY_NAME)

