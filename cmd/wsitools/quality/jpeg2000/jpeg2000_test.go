package jpeg2000

import (
	"encoding/binary"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

// buildMinimalCodestream constructs the smallest valid-looking
// codestream we need for inspector testing: SOC + SIZ + COD + QCD.
// transformType: 0 = irreversible (lossy 9/7), 1 = reversible (lossless 5/3)
// layers: number of progressive layers
// decompLevels: number of decomposition levels
func buildMinimalCodestream(transformType byte, layers, decompLevels int) []byte {
	var b []byte
	// SOC: 0xFF4F (no payload).
	b = append(b, 0xFF, 0x4F)
	// SIZ marker (0xFF51) + length (38) + 36 bytes of payload (minimal).
	siz := make([]byte, 36)
	b = append(b, 0xFF, 0x51, 0x00, 0x26)
	b = append(b, siz...)
	// COD marker (0xFF52) + length (12) + payload:
	//   1 byte Scod (coding style; 0)
	//   1 byte progression order (0)
	//   2 bytes layers (big-endian)
	//   1 byte multi-comp transform (0)
	//   1 byte decomposition levels
	//   1 byte code-block width exp (4 → 64)
	//   1 byte code-block height exp (4 → 64)
	//   1 byte code-block style (0)
	//   1 byte transform (0 = irreversible 9/7, 1 = reversible 5/3)
	codPayload := make([]byte, 10)
	codPayload[0] = 0 // Scod
	codPayload[1] = 0 // progression
	binary.BigEndian.PutUint16(codPayload[2:4], uint16(layers))
	codPayload[4] = 0 // multi-comp
	codPayload[5] = byte(decompLevels)
	codPayload[6] = 4
	codPayload[7] = 4
	codPayload[8] = 0
	codPayload[9] = transformType
	b = append(b, 0xFF, 0x52, 0x00, byte(2+len(codPayload)))
	b = append(b, codPayload...)
	// QCD marker (0xFF5C) + length (3) + 1-byte payload (minimal Sqcd).
	b = append(b, 0xFF, 0x5C, 0x00, 0x03, 0x00)
	return b
}

func TestJP2KInspectorRegistered(t *testing.T) {
	insp, ok := quality.For(opentile.CompressionJP2K)
	if !ok {
		t.Fatalf("JP2K inspector not registered")
	}
	if insp.Compression() != opentile.CompressionJP2K {
		t.Errorf("Compression(): got %v, want JP2K", insp.Compression())
	}
}

func TestInspectJP2KReversible(t *testing.T) {
	insp := &inspector{}
	bytes := buildMinimalCodestream(1, 1, 4) // reversible, 1 layer, 4 decomp
	info, err := insp.Inspect(bytes)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !info.Lossless {
		t.Errorf("Lossless: got false, want true (reversible transform)")
	}
	if info.Codec != "JPEG 2000" {
		t.Errorf("Codec: got %q, want \"JPEG 2000\"", info.Codec)
	}
	if info.LayerCount != 1 {
		t.Errorf("LayerCount: got %d, want 1", info.LayerCount)
	}
}

func TestInspectJP2KIrreversible(t *testing.T) {
	insp := &inspector{}
	bytes := buildMinimalCodestream(0, 4, 5) // irreversible, 4 layers, 5 decomp
	info, err := insp.Inspect(bytes)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Lossless {
		t.Errorf("Lossless: got true, want false (irreversible transform)")
	}
	if info.LayerCount != 4 {
		t.Errorf("LayerCount: got %d, want 4", info.LayerCount)
	}
}

func TestInspectJP2KNotACodestream(t *testing.T) {
	insp := &inspector{}
	_, err := insp.Inspect([]byte("not a codestream"))
	if err == nil {
		t.Error("expected error on non-JP2K bytes")
	}
}

func TestInspectJP2KMissingCOD(t *testing.T) {
	insp := &inspector{}
	// SOC only.
	_, err := insp.Inspect([]byte{0xFF, 0x4F})
	if err == nil {
		t.Error("expected error on codestream missing COD")
	}
}
