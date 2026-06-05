package logic

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
)

func generateThumbnail(data []byte, maxWidth, maxHeight int32) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image failed: %w", err)
	}

	srcBounds := img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}

	newW, newH := calcThumbSize(int32(srcW), int32(srcH), maxWidth, maxHeight)
	thumbnail := resizeBilinear(img, int(newW), int(newH))

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumbnail, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("encode thumbnail failed: %w", err)
	}
	return buf.Bytes(), nil
}

func calcThumbSize(srcW, srcH, maxW, maxH int32) (int32, int32) {
	if srcW <= maxW && srcH <= maxH {
		return srcW, srcH
	}
	ratioW := float64(maxW) / float64(srcW)
	ratioH := float64(maxH) / float64(srcH)
	ratio := ratioW
	if ratioH < ratioW {
		ratio = ratioH
	}
	return int32(float64(srcW) * ratio), int32(float64(srcH) * ratio)
}

func resizeBilinear(src image.Image, newW, newH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	xRatio := float64(srcW-1) / float64(max(newW-1, 1))
	yRatio := float64(srcH-1) / float64(max(newH-1, 1))

	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			px := float64(x) * xRatio
			py := float64(y) * yRatio

			x0 := int(px)
			y0 := int(py)
			x1 := min(x0+1, srcW-1)
			y1 := min(y0+1, srcH-1)

			xFrac := px - float64(x0)
			yFrac := py - float64(y0)

			r, g, b, a := bilinearRGBA(src, x0, y0, x1, y1, xFrac, yFrac)

			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

func bilinearRGBA(src image.Image, x0, y0, x1, y1 int, xFrac, yFrac float64) (uint32, uint32, uint32, uint32) {
	c00r, c00g, c00b, c00a := src.At(x0, y0).RGBA()
	c10r, c10g, c10b, c10a := src.At(x1, y0).RGBA()
	c01r, c01g, c01b, c01a := src.At(x0, y1).RGBA()
	c11r, c11g, c11b, c11a := src.At(x1, y1).RGBA()

	r := bilinearInterp(c00r, c10r, c01r, c11r, xFrac, yFrac)
	g := bilinearInterp(c00g, c10g, c01g, c11g, xFrac, yFrac)
	b := bilinearInterp(c00b, c10b, c01b, c11b, xFrac, yFrac)
	a := bilinearInterp(c00a, c10a, c01a, c11a, xFrac, yFrac)
	return r, g, b, a
}

func bilinearInterp(c00, c10, c01, c11 uint32, xFrac, yFrac float64) uint32 {
	top := uint32(float64(c00)*(1-xFrac) + float64(c10)*xFrac)
	bottom := uint32(float64(c01)*(1-xFrac) + float64(c11)*xFrac)
	return uint32(float64(top)*(1-yFrac) + float64(bottom)*yFrac)
}
