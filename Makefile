.PHONY: test vet cover bench install clean goldens-byte-stable bench-dzi

GO ?= go
BIN = bin/wsitools

test:
	$(GO) test ./... -race -count=1

vet:
	$(GO) vet ./...

cover:
	$(GO) test ./... -race -count=1 -coverprofile=coverage.txt -covermode=atomic
	$(GO) tool cover -func=coverage.txt | tail -1

bench:
	$(GO) test ./tests/bench/... -bench=. -benchmem -run=^$$

install:
	$(GO) install ./cmd/wsitools

build:
	$(GO) build -o $(BIN) ./cmd/wsitools

clean:
	rm -rf bin/ coverage.txt

# Asserts that transcode output is byte-identical across runs and across
# GOMAXPROCS. Requires WSI_TOOLS_TESTDIR to point at a directory containing
# svs/CMU-1-Small-Region.svs (and any other fixtures you want to gate on).
goldens-byte-stable: build
	@if [ -z "$$WSI_TOOLS_TESTDIR" ]; then \
		echo "WSI_TOOLS_TESTDIR not set; skipping byte-stability check"; \
		exit 0; \
	fi
	@for SAMPLE in $$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs; do \
		[ -f "$$SAMPLE" ] || { echo "missing $$SAMPLE; skipping"; continue; }; \
		echo "=== byte-stability on $$SAMPLE ==="; \
		OUT1=$$(mktemp -t out1.XXXXXX).svs; \
		OUT2=$$(mktemp -t out2.XXXXXX).svs; \
		OUT3=$$(mktemp -t out3.XXXXXX).svs; \
		GOMAXPROCS=2  ./bin/wsitools transcode --codec jpeg --container svs -f -o "$$OUT1" "$$SAMPLE" >/dev/null; \
		GOMAXPROCS=8  ./bin/wsitools transcode --codec jpeg --container svs -f -o "$$OUT2" "$$SAMPLE" >/dev/null; \
		GOMAXPROCS=32 ./bin/wsitools transcode --codec jpeg --container svs -f -o "$$OUT3" "$$SAMPLE" >/dev/null; \
		H1=$$(shasum -a 256 "$$OUT1" | awk '{print $$1}'); \
		H2=$$(shasum -a 256 "$$OUT2" | awk '{print $$1}'); \
		H3=$$(shasum -a 256 "$$OUT3" | awk '{print $$1}'); \
		rm -f "$$OUT1" "$$OUT2" "$$OUT3"; \
		[ "$$H1" = "$$H2" ] && [ "$$H2" = "$$H3" ] || { \
			echo "FAIL byte-stability on $$SAMPLE: $$H1 / $$H2 / $$H3" >&2; \
			exit 1; \
		}; \
		echo "OK $$H1"; \
	done

bench-dzi: $(BIN)
	@scripts/bench-dzi.sh
