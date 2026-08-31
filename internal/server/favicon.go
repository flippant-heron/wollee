package server

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"

	appservice "github.com/flippant-heron/wollee/internal/service"
)

// faviconSize is the square dimension (in pixels) browsers expect for a
// favicon; the configured logo is typically much larger (and often not
// square), so it must be downscaled/cropped rather than served as-is.
const faviconSize = 32

// Favicon generation modes for config.LogoConfig.FaviconMode.
const (
	FaviconModeResize       = "resize"        // stretch both dimensions to fit the square
	FaviconModeResizeLeft   = "resize-left"   // fit height, crop width from the left
	FaviconModeResizeRight  = "resize-right"  // fit height, crop width from the right
	FaviconModeResizeTop    = "resize-top"    // fit width, crop height from the top
	FaviconModeResizeBottom = "resize-bottom" // fit width, crop height from the bottom
)

// DefaultFaviconMode is used when config.LogoConfig.FaviconMode is empty or
// unrecognized.
const DefaultFaviconMode = FaviconModeResizeLeft

// generateFavicon decodes an arbitrary raster logo image and produces a
// faviconSize x faviconSize PNG according to mode:
//   - "resize" stretches both dimensions independently to fit the square.
//   - "resize-left"/"resize-right" scale the image so its height matches
//     faviconSize, preserving aspect ratio, then crop the width down to
//     faviconSize from the left or right edge respectively.
//   - "resize-top"/"resize-bottom" scale the image so its width matches
//     faviconSize, preserving aspect ratio, then crop the height down to
//     faviconSize from the top or bottom edge respectively.
//
// The result is served under /favicon.ico; browsers sniff the actual
// content type rather than relying on the URL extension, so a PNG payload
// there is standard practice.
func generateFavicon(data []byte, mode string, logger *appservice.Logger) (favData []byte, contentType string) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		logger.Error("decode logo image for favicon", err)
		return nil, ""
	}

	switch mode {
	case FaviconModeResize, FaviconModeResizeLeft, FaviconModeResizeRight, FaviconModeResizeTop, FaviconModeResizeBottom:
	default:
		if mode != "" {
			logger.Warning("unknown logo.faviconMode, falling back to default", "mode", mode, "default", DefaultFaviconMode)
		}
		mode = DefaultFaviconMode
	}

	var out image.Image
	if mode == FaviconModeResize {
		out = resizeRect(src, faviconSize, faviconSize)
	} else {
		out = resizeAndCrop(src, faviconSize, mode)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		logger.Error("encode favicon png", err)
		return nil, ""
	}
	return buf.Bytes(), "image/png"
}

// resizeAndCrop scales src, preserving aspect ratio, so that the dimension
// not being cropped exactly matches size, then crops the other dimension
// down to size from the edge indicated by mode. If the scaled image ends up
// smaller than size along the cropped dimension (e.g. a portrait logo asked
// to crop horizontally), the result is left-/top-aligned within the
// returned square and the remainder is left transparent.
func resizeAndCrop(src image.Image, size int, mode string) image.Image {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()

	var resizedW, resizedH int
	if mode == FaviconModeResizeTop || mode == FaviconModeResizeBottom {
		resizedW = size
		resizedH = int(math.Round(float64(srcH) * float64(size) / float64(srcW)))
	} else {
		resizedH = size
		resizedW = int(math.Round(float64(srcW) * float64(size) / float64(srcH)))
	}
	if resizedW < 1 {
		resizedW = 1
	}
	if resizedH < 1 {
		resizedH = 1
	}
	resized := resizeRect(src, resizedW, resizedH)

	cropX, cropY := 0, 0
	switch mode {
	case FaviconModeResizeRight:
		cropX = resizedW - size
	case FaviconModeResizeBottom:
		cropY = resizedH - size
	}
	if cropX < 0 {
		cropX = 0
	}
	if cropY < 0 {
		cropY = 0
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), resized, image.Pt(cropX, cropY), draw.Src)
	return dst
}

// resizeRect scales src to exactly dstW x dstH using box averaging (mean of
// the source pixels mapped to each destination pixel). This avoids pulling
// in an external imaging library while still giving reasonably sharp
// results when shrinking an image down to icon size.
func resizeRect(src image.Image, dstW, dstH int) image.Image {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	for y := 0; y < dstH; y++ {
		srcY0 := bounds.Min.Y + y*srcH/dstH
		srcY1 := bounds.Min.Y + (y+1)*srcH/dstH
		if srcY1 <= srcY0 {
			srcY1 = srcY0 + 1
		}
		for x := 0; x < dstW; x++ {
			srcX0 := bounds.Min.X + x*srcW/dstW
			srcX1 := bounds.Min.X + (x+1)*srcW/dstW
			if srcX1 <= srcX0 {
				srcX1 = srcX0 + 1
			}
			dst.Set(x, y, averagePixel(src, srcX0, srcX1, srcY0, srcY1, bounds))
		}
	}
	return dst
}

// averagePixel returns the mean color of src over [x0,x1) x [y0,y1), clamped
// to bounds.
func averagePixel(src image.Image, x0, x1, y0, y1 int, bounds image.Rectangle) color.RGBA {
	var r, g, b, a, count uint64
	for sy := y0; sy < y1 && sy < bounds.Max.Y; sy++ {
		for sx := x0; sx < x1 && sx < bounds.Max.X; sx++ {
			pr, pg, pb, pa := src.At(sx, sy).RGBA()
			r += uint64(pr)
			g += uint64(pg)
			b += uint64(pb)
			a += uint64(pa)
			count++
		}
	}
	if count == 0 {
		count = 1
	}
	return color.RGBA{
		R: uint8((r / count) >> 8),
		G: uint8((g / count) >> 8),
		B: uint8((b / count) >> 8),
		A: uint8((a / count) >> 8),
	}
}
