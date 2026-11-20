package trayui

import (
	"encoding/binary"
	"image"
	"testing"
)

func TestEncodeICO_HeaderAndEntries(t *testing.T) {
	imgs := []*image.NRGBA{
		Badge(32, 0.5, StateOK, testPalette, ""),
		Badge(16, 0.5, StateOK, testPalette, ""),
		Badge(24, 0.5, StateOK, testPalette, ""),
	}
	ico := EncodeICO(imgs)
	if len(ico) < 6 {
		t.Fatalf("ico too short: %d bytes", len(ico))
	}
	if got := binary.LittleEndian.Uint16(ico[0:]); got != 0 {
		t.Errorf("reserved = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(ico[2:]); got != 1 {
		t.Errorf("type = %d, want 1 (icon)", got)
	}
	count := int(binary.LittleEndian.Uint16(ico[4:]))
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	// Записи сортируются по размеру: оболочка ожидает возрастающий порядок.
	wantSizes := []int{16, 24, 32}
	for i := 0; i < count; i++ {
		e := ico[6+i*16:]
		if int(e[0]) != wantSizes[i] || int(e[1]) != wantSizes[i] {
			t.Errorf("entry %d size = %dx%d, want %d", i, e[0], e[1], wantSizes[i])
		}
		if p := binary.LittleEndian.Uint16(e[4:]); p != 1 {
			t.Errorf("entry %d planes = %d, want 1", i, p)
		}
		if b := binary.LittleEndian.Uint16(e[6:]); b != 32 {
			t.Errorf("entry %d bitcount = %d, want 32", i, b)
		}
		size := int(binary.LittleEndian.Uint32(e[8:]))
		off := int(binary.LittleEndian.Uint32(e[12:]))
		if off+size > len(ico) {
			t.Fatalf("entry %d payload out of bounds: off=%d size=%d total=%d", i, off, size, len(ico))
		}
	}
}

// Payload записи — BITMAPINFOHEADER с удвоенной высотой (XOR + AND маска)
// и строками снизу вверх.
func TestEncodeICO_BitmapPayload(t *testing.T) {
	const side = 16
	img := Badge(side, 1, StateOK, testPalette, "")
	ico := EncodeICO([]*image.NRGBA{img})
	off := int(binary.LittleEndian.Uint32(ico[6+12:]))
	payload := ico[off:]

	if got := binary.LittleEndian.Uint32(payload[0:]); got != 40 {
		t.Errorf("biSize = %d, want 40", got)
	}
	if got := int32(binary.LittleEndian.Uint32(payload[4:])); got != side {
		t.Errorf("biWidth = %d, want %d", got, side)
	}
	if got := int32(binary.LittleEndian.Uint32(payload[8:])); got != side*2 {
		t.Errorf("biHeight = %d, want %d (XOR+AND)", got, side*2)
	}
	if got := binary.LittleEndian.Uint16(payload[14:]); got != 32 {
		t.Errorf("biBitCount = %d, want 32", got)
	}

	xorBase := 40
	maskStride := ((side + 31) / 32) * 4
	maskBase := xorBase + side*side*4
	wantSize := 40 + side*side*4 + maskStride*side
	if len(payload) != wantSize {
		t.Fatalf("payload = %d bytes, want %d", len(payload), wantSize)
	}

	// Левый нижний угол значка прозрачен: в XOR-строках (снизу вверх)
	// это первая строка, в маске — выставленный бит.
	if a := payload[xorBase+3]; a != 0 {
		t.Errorf("bottom-left pixel alpha = %d, want 0", a)
	}
	if b := payload[maskBase] & 0x80; b == 0 {
		t.Error("AND mask: transparent pixel must have bit set")
	}
}

func TestEncodeICO_IgnoresInvalidInput(t *testing.T) {
	if got := EncodeICO(nil); got != nil {
		t.Errorf("nil input = %v, want nil", got)
	}
	// Не квадрат и нулевой размер отбрасываются.
	bad := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	if got := EncodeICO([]*image.NRGBA{bad, nil}); got != nil {
		t.Errorf("all-invalid input = %d bytes, want nil", len(got))
	}
}
