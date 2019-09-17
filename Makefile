PKG=./

cover: test
	go tool cover -html=./coverage.out


test:
	go test -race -coverprofile=./coverage.out


check: check-go check-lint

check-go:
	for P in ${PKG}; do \
		go test -coverprofile=coverage.out `$$P`; \
	done

check-lint:
	for P in ${PKG}; do \
		golint -set_exit_status `$$P`; \
	done

download-tools:
	go get -u golang.org/x/lint/golint \

.PHONY: check-go check-lint download-tools
