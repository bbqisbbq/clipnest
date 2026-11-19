//go:build windows

package clip

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
)

const (
	biRGB            = 0
	biBitFields      = 3
	biAlphaBitFields = 6
)

// dibToImage decodes a Windows device independent bitmap (CF_DIB / CF_DIBV5).
//
// Supported: 1/4/8/16/24/32 bpp with BI_RGB, 32 bpp with BI_BITFIELDS (treated
// as BGRA). 16 bpp bitfields are reported as unsupported rather than decoded
// wrongly.
func dibToImage(data []byte) (*image.NRGBA, error) {
	if len(data) < 16 {
		return nil, errors.New("DIB too short")
	}
	headerSize := binary.LittleEndian.Uint32(data[0:4])

	var width, height int
	var bitCount uint16
	var compression uint32
	var paletteBytes int
	pixelOffset := 0

	switch headerSize {
	case 12: // BITMAPCOREHEADER
		width = int(int16(binary.LittleEndian.Uint16(data[4:6])))
		height = int(int16(binary.LittleEndian.Uint16(data[6:8])))
		bitCount = binary.LittleEndian.Uint16(data[10:12])
		compression = biRGB
		pixelOffset = 12
	case 40, 52, 56, 108, 124: // INFOHEADER .. V5HEADER
		if len(data) < 40 {
			return nil, errors.New("DIB header truncated")
		}
		width = int(int32(binary.LittleEndian.Uint32(data[4:8])))
		height = int(int32(binary.LittleEndian.Uint32(data[8:12])))
		bitCount = binary.LittleEndian.Uint16(data[14:16])
		compression = binary.LittleEndian.Uint32(data[16:20])
		clrUsed := binary.LittleEndian.Uint32(data[32:36])
		pixelOffset = int(headerSize)
		if bitCount <= 8 {
			n := int(clrUsed)
			if n == 0 {
				n = 1 << bitCount
			}
			paletteBytes = n * 4
		} else if compression == biBitFields || compression == biAlphaBitFields {
			// Mask table lives between the 40-byte core header and pixel data.
			if headerSize == 40 {
				pixelOffset = 40 + 12
			}
		}
	default:
		return nil, errors.New("unsupported DIB header size")
	}

	if width <= 0 || height == 0 {
		return nil, errors.New("invalid DIB dimensions")
	}
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width > 20000 || height > 20000 {
		return nil, errors.New("DIB dimensions out of range")
	}

	if compression != biRGB && compression != biBitFields && compression != biAlphaBitFields {
		return nil, errors.New("compressed DIB not supported")
	}

	stride := ((width*int(bitCount) + 31) / 32) * 4
	start := pixelOffset + paletteBytes
	if start >= len(data) {
		return nil, errors.New("DIB pixel data missing")
	}
	needed := stride * height
	if start+needed > len(data) {
		// Some producers truncate the final row padding; clamp what we can.
		needed = len(data) - start
		if needed <= 0 {
			return nil, errors.New("DIB pixel data truncated")
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	pix := data[start:]

	rowFor := func(y int) int {
		if topDown {
			return y * stride
		}
		return (height - 1 - y) * stride
	}

	switch bitCount {
	case 32:
		anyAlpha := false
		for i := 0; i+3 < len(pix); i += 4 {
			if pix[i+3] != 0 {
				anyAlpha = true
				break
			}
		}
		for y := 0; y < height; y++ {
			off := rowFor(y)
			for x := 0; x < width; x++ {
				p := off + x*4
				if p+3 >= len(pix) {
					break
				}
				a := pix[p+3]
				if !anyAlpha {
					a = 0xFF
				}
				c := color.NRGBA{R: pix[p+2], G: pix[p+1], B: pix[p], A: a}
				if a != 0xFF && a != 0 {
					// DIB stores premultiplied-looking values for some apps;
					// un-premultiply so the PNG looks right in the browser.
					c.R = unpremultiply(c.R, a)
					c.G = unpremultiply(c.G, a)
					c.B = unpremultiply(c.B, a)
				}
				img.SetNRGBA(x, y, c)
			}
		}
	case 24:
		for y := 0; y < height; y++ {
			off := rowFor(y)
			for x := 0; x < width; x++ {
				p := off + x*3
				if p+2 >= len(pix) {
					break
				}
				img.SetNRGBA(x, y, color.NRGBA{R: pix[p+2], G: pix[p+1], B: pix[p], A: 0xFF})
			}
		}
	case 8:
		if paletteBytes == 0 || start < 4 {
			return nil, errors.New("8bpp DIB without palette")
		}
		pal := data[pixelOffset : pixelOffset+paletteBytes]
		for y := 0; y < height; y++ {
			off := rowFor(y)
			for x := 0; x < width; x++ {
				p := off + x
				if p >= len(pix) {
					break
				}
				idx := int(pix[p])
				q := idx * 4
				if q+3 >= len(pal) {
					continue
				}
				img.SetNRGBA(x, y, color.NRGBA{R: pal[q+2], G: pal[q+1], B: pal[q], A: 0xFF})
			}
		}
	case 1, 4:
		if paletteBytes == 0 {
			return nil, errors.New("indexed DIB without palette")
		}
		pal := data[pixelOffset : pixelOffset+paletteBytes]
		perByte := 8 / int(bitCount)
		mask := byte((1 << bitCount) - 1)
		for y := 0; y < height; y++ {
			off := rowFor(y)
			for x := 0; x < width; x++ {
				p := off + x/perByte
				if p >= len(pix) {
					break
				}
				shift := uint(8 - int(bitCount)*(x%perByte+1))
				idx := int((pix[p] >> shift) & mask)
				q := idx * 4
				if q+3 >= len(pal) {
					continue
				}
				img.SetNRGBA(x, y, color.NRGBA{R: pal[q+2], G: pal[q+1], B: pal[q], A: 0xFF})
			}
		}
	default:
		return nil, errors.New("unsupported DIB bit depth")
	}
	return img, nil
}

func unpremultiply(v, a byte) byte {
	if a == 0 {
		return 0
	}
	out := int(v) * 255 / int(a)
	if out > 255 {
		out = 255
	}
	return byte(out)
}

// imageToDIB encodes an image as a 32 bpp bottom-up BI_RGB DIB (CF_DIB).
func imageToDIB(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	stride := w * 4
	pixels := make([]byte, stride*h)
	for y := 0; y < h; y++ {
		// Bottom-up: destination row 0 is the last source row.
		dst := (h - 1 - y) * stride
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p := dst + x*4
			pixels[p] = byte(bl >> 8)
			pixels[p+1] = byte(g >> 8)
			pixels[p+2] = byte(r >> 8)
			pixels[p+3] = byte(a >> 8)
		}
	}

	header := make([]byte, 40)
	binary.LittleEndian.PutUint32(header[0:4], 40)
	binary.LittleEndian.PutUint32(header[4:8], uint32(int32(w)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(int32(h)))
	binary.LittleEndian.PutUint16(header[12:14], 1)
	binary.LittleEndian.PutUint16(header[14:16], 32)
	binary.LittleEndian.PutUint32(header[16:20], biRGB)
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(pixels)))

	return append(header, pixels...)
}
