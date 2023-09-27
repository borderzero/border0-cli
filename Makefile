GOCMD=go
GOBUILD=$(GOCMD) build
GOFMT=$(GOCMD)fmt
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=border0
BUCKET=pub-mysocketctl-bin

DATE := $(shell git log -1 --format=%cd --date=format:"%Y%m%d")
# BORDER0_VERSION is supplied by the build script
BORDER0_VERSION ?= $(git describe --long --dirty --tags)
VERSION=$(BORDER0_VERSION)
# strip debugging information with -s and -w linker flags
# -s: disable symbol table
# -w: disable DWARF generation
FLAGS := -ldflags "-s -w -X github.com/borderzero/border0-cli/cmd.version=$(VERSION) -X github.com/borderzero/border0-cli/cmd.date=$(DATE)"

all: lint moddownload test build

# Release for all platforms.
release:
	# Release for Windows 64bit
	shasum -a 256 ./bin/$(BINARY_NAME)_windows_amd64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_windows_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_windows_amd64-sha256-checksum.txt ${BUCKET} windows_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_windows_amd64 ${BUCKET} windows_amd64/$(BINARY_NAME).exe
	# Release for Windows ARM 64bit
	shasum -a 256 ./bin/$(BINARY_NAME)_windows_arm64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_windows_arm64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_windows_arm64-sha256-checksum.txt ${BUCKET} windows_arm64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_windows_arm64 ${BUCKET} windows_arm64/$(BINARY_NAME).exe
	# Release for Linux 64bit
	shasum -a 256 ./bin/$(BINARY_NAME)_linux_amd64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_linux_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_amd64-sha256-checksum.txt ${BUCKET} linux_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_amd64 ${BUCKET} linux_amd64/$(BINARY_NAME)
	# Release for Linux 32bit
	shasum -a 256 ./bin/$(BINARY_NAME)_linux_386 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_linux_386-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_386-sha256-checksum.txt ${BUCKET} linux_386/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_386 ${BUCKET} linux_386/$(BINARY_NAME)
	# Release for Raspberry Pi 64bit
	shasum -a 256 ./bin/$(BINARY_NAME)_linux_arm64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_linux_arm64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_arm64-sha256-checksum.txt ${BUCKET} linux_arm64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_arm64 ${BUCKET} linux_arm64/$(BINARY_NAME)
	# Release for Raspberry Pi 32bit
	shasum -a 256 ./bin/$(BINARY_NAME)_linux_arm | awk '{print $$1}' > ./bin/$(BINARY_NAME)_linux_arm-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_arm-sha256-checksum.txt ${BUCKET} linux_arm/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_arm ${BUCKET} linux_arm/$(BINARY_NAME)
	# Release for Raspberry Pi ARM v6 32bit
	shasum -a 256 ./bin/$(BINARY_NAME)_linux_armv6 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_linux_armv6-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_armv6-sha256-checksum.txt ${BUCKET} linux_armv6/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_linux_armv6 ${BUCKET} linux_armv6/$(BINARY_NAME)
	# Release for Intel Mac
	shasum -a 256 ./bin/$(BINARY_NAME)_darwin_amd64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_darwin_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_darwin_amd64-sha256-checksum.txt ${BUCKET} darwin_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_darwin_amd64 ${BUCKET} darwin_amd64/$(BINARY_NAME)
	# Release for Apple Silicon Mac
	shasum -a 256 ./bin/$(BINARY_NAME)_darwin_arm64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_darwin_arm64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_darwin_arm64-sha256-checksum.txt ${BUCKET} darwin_arm64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_darwin_arm64 ${BUCKET} darwin_arm64/$(BINARY_NAME)
	# Release for OpenBSD 64bit
	shasum -a 256 ./bin/$(BINARY_NAME)_openbsd_amd64 | awk '{print $$1}' > ./bin/$(BINARY_NAME)_openbsd_amd64-sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_openbsd_amd64-sha256-checksum.txt ${BUCKET} openbsd_amd64/sha256-checksum.txt
	python3 ./s3upload.py ./bin/$(BINARY_NAME)_openbsd_amd64 ${BUCKET} openbsd_amd64/$(BINARY_NAME)
	# Publish the latest version checksum file
	echo ${VERSION} > latest_version.txt
	python3 ./s3upload.py latest_version.txt ${BUCKET} latest_version.txt
	rm latest_version.txt

moddownload:
	go mod tidy
	go mod download

build:
	$(GOBUILD) $(FLAGS) -o $(BINARY_NAME) -v

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(FLAGS) -o $(BINARY_NAME) -v

# Cross compile for all supported platforms in parallel
build-all: build-windows-amd64 build-windows-arm64 build-linux-amd64 build-linux-arm64 build-linux-arm build-linux-armv6 build-linux-386 build-darwin-amd64 build-darwin-arm64 build-openbsd-amd64

build-windows-amd64:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_windows_amd64

build-windows-arm64:
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_windows_arm64

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_amd64

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_arm64

build-linux-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_arm

build-linux-armv6:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_armv6

build-linux-386:
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_linux_386

build-darwin-amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_darwin_amd64

build-darwin-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_darwin_arm64

build-openbsd-amd64:
	CGO_ENABLED=0 GOOS=openbsd GOARCH=amd64 go build $(FLAGS) -o ./bin/$(BINARY_NAME)_openbsd_amd64

build-linux-multiarch: build-linux-amd64 build-linux-arm64 build-linux-arm build-linux-armv6 build-linux-386

deb-package-amd64:
	./build-deb.sh $(VERSION) amd64

deb-package-arm64:
	./build-deb.sh $(VERSION) arm64

deb-package-arm:
	./build-deb.sh $(VERSION) arm

deb-package-armv6:
	./build-deb.sh $(VERSION) armv6

deb-package-386:
	./build-deb.sh $(VERSION) 386

deb-package-multiarch:
	@echo "Creating DEB packages under ./repos repository structure"
	./build-deb.sh $(VERSION) amd64
	./build-deb.sh $(VERSION) arm64
	./build-deb.sh $(VERSION) arm
	./build-deb.sh $(VERSION) armv6
	./build-deb.sh $(VERSION) 386

deb-repository:
	@echo "Creating DEB repo and signing it"
	./generate-deb-repo.sh

rpm-package-amd64:
	./build-rpm.sh $(VERSION) amd64

rpm-package-arm64:
	./build-rpm.sh $(VERSION) arm64

rpm-package-arm:
	./build-rpm.sh $(VERSION) arm

rpm-package-armv6:
	./build-rpm.sh $(VERSION) armv6

rpm-package-386:
	./build-rpm.sh $(VERSION) 386

rpm-package-multiarch:
	@echo "Creating DEB packages under ./repos repository structure"
	./build-rpm.sh $(VERSION) amd64
	./build-rpm.sh $(VERSION) arm64
	./build-rpm.sh $(VERSION) arm
	./build-rpm.sh $(VERSION) armv6
	./build-rpm.sh $(VERSION) 386

rpm-repository:
	@echo "Creating repo for RPM"
	./generate-rpm-repo.sh

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
