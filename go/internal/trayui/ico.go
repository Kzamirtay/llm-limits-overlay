package trayui

import (
	"bytes"
	"encoding/binary"
	"image"
	"sort"
)

// EncodeICO пакует изображения в .ico с несжатыми BMP-записями (32bpp BGRA +
// AND-маска). PNG-внутри-ICO короче, но поддерживается оболочкой через раз;
// распакованный BMP читает любой LoadImage начиная с XP, а размер значка
// (несколько КБ) роли не играет.
//
// Изображения должны быть квадратными; порядок входа не важен — записи
// сортируются по размеру, как ожидает проводник.
func EncodeICO(imgs []*image.NRGBA) []byte {
	list := make([]*image.NRGBA, 0, len(imgs))
	for _, im := range imgs {
		if im != nil && im.Rect.Dx() > 0 && im.Rect.Dx() == im.Rect.Dy() {
			list = append(list, im)
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].Rect.Dx() < list[j].Rect.Dx()
	})

	const (
		iconDirSize   = 6
		iconEntrySize = 16
		bmpHeaderSize = 40
	)
	offsets := make([]int, len(list))
	payloads := make([][]byte, len(list))
	offset := iconDirSize + iconEntrySize*len(list)
	for i, im := range list {
		payloads[i] = encodeBMPEntry(im, bmpHeaderSize)
		offsets[i] = offset
		offset += len(payloads[i])
	}

	var buf bytes.Buffer
	buf.Grow(offset)
	// ICONDIR
	writeU16(&buf, 0) // reserved
	writeU16(&buf, 1) // type = icon
	writeU16(&buf, uint16(len(list)))

	maxSide := 256
	for i, im := range list {
		side := im.Rect.Dx()
		if side >= maxSide {
			writeU8(&buf, 0) // 256 и больше кодируются как 0
			writeU8(&buf, 0)
		} else {
			writeU8(&buf, uint8(side))
			writeU8(&buf, uint8(side))
		}
		writeU8(&buf, 0)  // color count: 0 для 32bpp
		writeU8(&buf, 0)  // reserved
		writeU16(&buf, 1) // planes
		writeU16(&buf, 32)
		writeU32(&buf, uint32(len(payloads[i])))
		writeU32(&buf, uint32(offsets[i]))
	}
	for _, p := range payloads {
		buf.Write(p)
	}
	return buf.Bytes()
}

// encodeBMPEntry собирает payload записи: BITMAPINFOHEADER с удвоенной высотой
// (XOR + AND маска), строки снизу вверх, как требует формат .ico.
func encodeBMPEntry(im *image.NRGBA, headerSize int) []byte {
	w, h := im.Rect.Dx(), im.Rect.Dy()
	maskStride := ((w + 31) / 32) * 4 // AND-маска: 1 бит на пиксель, выравнивание по 4 байта
	payload := make([]byte, headerSize+w*h*4+maskStride*h)

	binary.LittleEndian.PutUint32(payload[0:], uint32(headerSize)) // biSize
	binary.LittleEndian.PutUint32(payload[4:], uint32(w))          // biWidth
	binary.LittleEndian.PutUint32(payload[8:], uint32(h*2))        // biHeight: XOR + AND
	binary.LittleEndian.PutUint16(payload[12:], 1)                 // biPlanes
	binary.LittleEndian.PutUint16(payload[14:], 32)                // biBitCount
	binary.LittleEndian.PutUint32(payload[16:], 0)                 // biCompression = BI_RGB
	binary.LittleEndian.PutUint32(payload[20:], uint32(w*h*4))     // biSizeImage

	// XOR-биты: BGRA, снизу вверх. Непрозрачность несёт альфа (32bpp+alpha),
	// но AND-маску всё равно заполняем: часть оболочек её всё ещё смотрит.
	xorBase := headerSize
	maskBase := xorBase + w*h*4
	for y := 0; y < h; y++ {
		row := (h - 1 - y) * im.Stride
		dst := xorBase + y*w*4
		maskRow := maskBase + y*maskStride
		for x := 0; x < w; x++ {
			src := row + x*4
			r := im.Pix[src+0]
			g := im.Pix[src+1]
			b := im.Pix[src+2]
			a := im.Pix[src+3]
			payload[dst+x*4+0] = b
			payload[dst+x*4+1] = g
			payload[dst+x*4+2] = r
			payload[dst+x*4+3] = a
			if a == 0 {
				payload[maskRow+x/8] |= 1 << (7 - uint(x%8))
			}
		}
	}
	return payload
}

func writeU8(buf *bytes.Buffer, v uint8) { buf.WriteByte(v) }
func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}
func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}
