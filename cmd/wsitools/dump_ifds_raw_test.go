package main

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestDecodeValueShortScalar(t *testing.T) {
	raw := []byte{0x64, 0x00}
	got, err := decodeValue(3, 1, raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if got != uint64(100) {
		t.Errorf("got %v, want uint64(100)", got)
	}
}

func TestDecodeValueLongArray(t *testing.T) {
	raw := []byte{1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0}
	got, err := decodeValue(4, 3, raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecodeValueAsciiNulTrim(t *testing.T) {
	raw := []byte("hello\x00world\x00")
	got, err := decodeValue(2, uint64(len(raw)), raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestDecodeValueRationalArray(t *testing.T) {
	raw := []byte{
		1, 0, 0, 0, 72, 0, 0, 0,
		1, 0, 0, 0, 72, 0, 0, 0,
	}
	got, err := decodeValue(5, 2, raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1/72", "1/72"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecodeValueUnknownType(t *testing.T) {
	raw := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	got, err := decodeValue(99, 8, raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, raw) {
		t.Errorf("got %v, want raw bytes", got)
	}
}

func TestDecodeValueBytePassthrough(t *testing.T) {
	raw := []byte{0xde, 0xad, 0xbe, 0xef}
	got, err := decodeValue(1, 4, raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, raw) {
		t.Errorf("got %v, want raw bytes", got)
	}
}

func TestDecodeValueFloat(t *testing.T) {
	raw := []byte{0x00, 0x00, 0x80, 0x3f}
	got, err := decodeValue(11, 1, raw, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if got != float64(1.0) {
		t.Errorf("got %v, want 1.0", got)
	}
}
