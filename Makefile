.PHONY: test vet cover bench install clean goldens-byte-stable bench-dzi dicom-validate check-fixtures

GO ?= go
BIN = bin/wsitools
# Sentinel fixture used to detect a mis-pointed WSI_TOOLS_TESTDIR. When the var
# is set, integration tests gate on fixtures under it; a stale/wrong/empty dir
# (e.g. a path left over from a repo move) makes them all t.Skip() silently —
# which reads as a green run but tested nothing. check-fixtures fails loud
# instead. Point WSI_TOOLS_TESTDIR at your fixtures, or unset it to run unit-only.
FIXTURE_SENTINEL = svs/CMU-1-Small-Region.svs
# DICOM conformance validator. dciodvfy ships in David Clunie's dicom3tools
# (https://www.dclunie.com/dicom3tools.html — precompiled macOS/Windows binaries
# under workinprogress/, or build from source). Override if it is not on PATH,
# e.g. `make dicom-validate DCIODVFY=/tmp/dciodvfy`.
DCIODVFY ?= dciodvfy

# Fail loud when WSI_TOOLS_TESTDIR is set but doesn't look like a fixtures dir,
# so fixture-gated tests don't silently skip and masquerade as a pass. Unset is
# fine (fixture tests skip — the fresh-checkout / unit-only case).
check-fixtures:
	@if [ -n "$$WSI_TOOLS_TESTDIR" ]; then \
		if [ ! -d "$$WSI_TOOLS_TESTDIR" ]; then \
			echo "ERROR: WSI_TOOLS_TESTDIR=$$WSI_TOOLS_TESTDIR does not exist."; \
			echo "       Fixture-gated tests would all silently skip. Fix the path or 'unset WSI_TOOLS_TESTDIR'."; \
			exit 1; \
		fi; \
		if [ ! -f "$$WSI_TOOLS_TESTDIR/$(FIXTURE_SENTINEL)" ]; then \
			echo "ERROR: WSI_TOOLS_TESTDIR=$$WSI_TOOLS_TESTDIR is missing sentinel fixture $(FIXTURE_SENTINEL)."; \
			echo "       It's set but doesn't look like a fixtures dir; tests would silently skip."; \
			echo "       Point it at your fixtures (e.g. \"\$$(pwd)/sample_files\") or 'unset WSI_TOOLS_TESTDIR'."; \
			exit 1; \
		fi; \
	fi

test: check-fixtures
	$(GO) test ./... -race -count=1

vet:
	$(GO) vet ./...

cover: check-fixtures
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
		for L in "$$DIR"/pyr/*.dcm; do \
			echo "=== dciodvfy (DICOM pyramid) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR"; \
	else echo "missing $$SM; skipping DICOM pyramid"; fi; \
	SVS="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$SVS" ]; then \
		DIR3=$$(mktemp -d -t wsm-svs.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR3/pyr" "$$SVS"; \
		for L in "$$DIR3"/pyr/*.dcm; do \
			echo "=== dciodvfy (SVS pyramid+assoc) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR3"; \
	else echo "missing $$SVS; skipping SVS->DICOM"; fi; \
	JP2K="$$WSI_TOOLS_TESTDIR/dicom/3DHISTECH-JP2K"; \
	if [ -d "$$JP2K" ]; then \
		DIR2=$$(mktemp -d -t wsm-jp2k.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR2/pyr" "$$JP2K"; \
		for L in "$$DIR2"/pyr/*.dcm; do \
			echo "=== dciodvfy (JP2K pyramid) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR2"; \
	else echo "missing $$JP2K; skipping JP2K pyramid"; fi; \
	HTJ2K="$$WSI_TOOLS_TESTDIR/dicom/3DHISTECH-HTJ2K"; \
	if [ -d "$$HTJ2K" ]; then \
		DIR4=$$(mktemp -d -t wsm-htj2k.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR4/pyr" "$$HTJ2K"; \
		for L in "$$DIR4"/pyr/*.dcm; do \
			echo "=== dciodvfy (HTJ2K pyramid) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR4"; \
	else echo "missing $$HTJ2K; skipping HTJ2K pyramid"; fi; \
	LZW="$$WSI_TOOLS_TESTDIR/svs/590_crop_lzw_imagescope.tif"; \
	if [ -f "$$LZW" ]; then \
		DIR5=$$(mktemp -d -t wsm-lzw.XXXXXX); \
		./bin/wsitools convert --to dicom --codec jpeg -f -o "$$DIR5/pyr" "$$LZW"; \
		for L in "$$DIR5"/pyr/*.dcm; do \
			echo "=== dciodvfy (A4b LZW->JPEG re-encode) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR5"; \
	else echo "missing $$LZW; skipping A4b LZW->JPEG"; fi; \
	exit $$RC
