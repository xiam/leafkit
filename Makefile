.PHONY: fmt-check vet test

fmt-check:
	@unformatted=$$(gofmt -l .) || exit $$?; \
	if [ -n "$$unformatted" ]; then \
		printf 'Run gofmt on:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

test:
	go test -race -count=1 ./...
