.PHONY: test vet cover bench install clean goldens-byte-stable bench-dzi dicom-validate

GO ?= go
BIN = bin/wsitools
# DICOM conformance validator. dciodvfy ships in David Clunie's dicom3tools
# (https://www.dclunie.com/dicom3tools.html — precompiled macOS/Windows binaries
# under workinprogress/, or build from source). Override if it is not on PATH,
# e.g. `make dicom-validate DCIODVFY=/tmp/dciodvfy`.
DCIODVFY ?= dciodvfy

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

# Emits WSM VOLUME instances and runs dciodvfy conformance validation
# (Phase 0/1 de-risk): the FULL multi-instance pyramid from the Grundium fixture
# (every level-<n>.dcm — exercises reduced-level spatial metadata), and a
# non-DICOM SVS->DICOM instance from CMU-1-Small-Region.svs (RGB photometric +
# synthesized sRGB ICC path). Requires WSI_TOOLS_TESTDIR and dciodvfy (see
# DCIODVFY). Success bar: 0 Errors per instance (Study ID DICOMDIR warning is
# expected/benign); a non-zero exit from any instance fails the target.
dicom-validate: build
	@if [ -z "$$WSI_TOOLS_TESTDIR" ]; then \
		echo "WSI_TOOLS_TESTDIR not set; skipping dicom-validate"; \
		exit 0; \
	fi
	@command -v "$(DCIODVFY)" >/dev/null 2>&1 || { echo "$(DCIODVFY) not found; see Makefile DCIODVFY note"; exit 1; }; \
	RC=0; \
	SM="$$WSI_TOOLS_TESTDIR/dicom/scan_621_grundium_dicom"; \
	if [ -d "$$SM" ]; then \
		DIR=$$(mktemp -d -t wsm-pyr.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR/pyr" "$$SM"; \
		for L in "$$DIR"/pyr/level-*.dcm; do \
			echo "=== dciodvfy (DICOM pyramid) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR"; \
	else echo "missing $$SM; skipping DICOM pyramid"; fi; \
	SVS="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$SVS" ]; then \
		OUT2=$$(mktemp -t wsm-svs.XXXXXX).dcm; \
		./bin/wsitools convert --to dicom --level 0 -f -o "$$OUT2" "$$SVS"; \
		echo "=== dciodvfy (SVS->DICOM) $$OUT2 ==="; \
		"$(DCIODVFY)" "$$OUT2" || RC=$$?; \
		rm -f "$$OUT2"; \
	else echo "missing $$SVS; skipping SVS->DICOM"; fi; \
	exit $$RC
