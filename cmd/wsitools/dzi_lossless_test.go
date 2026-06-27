package main

import "testing"

func TestLosslessDZIConfig(t *testing.T) {
	cfg, err := losslessDZIConfig(losslessDZIInputs{
		isJPEG: true, srcTileSize: 256, factor: 1, rectSet: false,
		userSetTileSize: false, userSetOverlap: false,
		reqTileSize: 256, reqOverlap: 1,
	})
	if err != nil {
		t.Fatalf("valid lossless: %v", err)
	}
	if cfg.tileSize != 256 || cfg.overlap != 0 {
		t.Fatalf("auto-config: got tile=%d overlap=%d want 256/0", cfg.tileSize, cfg.overlap)
	}
}

func TestLosslessDZIConfig_Errors(t *testing.T) {
	base := losslessDZIInputs{isJPEG: true, srcTileSize: 240, factor: 1, rectSet: false, reqTileSize: 240, reqOverlap: 0}
	bad := base
	bad.isJPEG = false
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("non-jpeg source must error")
	}
	bad = base
	bad.factor = 2
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("--lossless + --factor must error")
	}
	bad = base
	bad.rectSet = true
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("--lossless + --rect must error")
	}
	bad = base
	bad.userSetOverlap = true
	bad.reqOverlap = 1
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("explicit --dzi-overlap 1 must error")
	}
	bad = base
	bad.userSetTileSize = true
	bad.reqTileSize = 512
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("explicit --tile-size != source must error")
	}
}
