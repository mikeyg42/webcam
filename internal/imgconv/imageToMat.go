// Package imgconv provides optimized image.Image to gocv.Mat conversion
// with correct handling of all edge cases including odd dimensions,
// non-zero origins, and premultiplied alpha.
package imgconv

import (
	"fmt"
	"image"
	"sync"

	"gocv.io/x/gocv"
)

// YCbCr to RGB lookup tables for fast conversion (BT.601 full range)
var (
	ycbcrOnce  sync.Once
	ycbcrTable struct {
		cr2r [256]int32 // Cr contribution to R
		cb2b [256]int32 // Cb contribution to B
		cr2g [256]int32 // Cr contribution to G
		cb2g [256]int32 // Cb contribution to G
	}
)

// ToMat converts any image.Image to OpenCV Mat in BGR format.
// Automatically unpremultiplies RGBA images to avoid dark halos.
// Returns a Mat you own - caller must Close() it.
func ToMat(img image.Image) (gocv.Mat, error) {
	if img == nil {
		return gocv.NewMat(), fmt.Errorf("imgconv: nil image")
	}

	bounds := img.Bounds()
	if bounds.Empty() {
		return gocv.NewMat(), fmt.Errorf("imgconv: empty image bounds")
	}

	// Route to optimal converter based on concrete type
	switch im := img.(type) {
	case *image.RGBA:
		return convertRGBA(im)
	case *image.NRGBA:
		return convertNRGBA(im)
	case *image.Gray:
		return convertGray(im)
	case *image.YCbCr:
		// All YCbCr formats use manual conversion for compatibility
		return convertYCbCrManual(im)
	default:
		return convertGeneric(img)
	}
}

// convertRGBA handles RGBA images with unpremultiplication to avoid dark halos
func convertRGBA(im *image.RGBA) (gocv.Mat, error) {
	w, h := im.Rect.Dx(), im.Rect.Dy()

	// Always unpremultiply to avoid dark halos around transparent objects
	buf := make([]byte, 4*w*h)
	dst := 0
	y0, x0 := im.Rect.Min.Y, im.Rect.Min.X

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (y+y0)*im.Stride + (x+x0)*4
			r, g, b, a := im.Pix[idx], im.Pix[idx+1], im.Pix[idx+2], im.Pix[idx+3]

			// Unpremultiply alpha: convert from premultiplied to straight alpha
			if a > 0 && a < 255 {
				// color = premultiplied * 255 / alpha
				r = uint8((uint32(r) * 255) / uint32(a))
				g = uint8((uint32(g) * 255) / uint32(a))
				b = uint8((uint32(b) * 255) / uint32(a))
			}

			buf[dst] = r
			buf[dst+1] = g
			buf[dst+2] = b
			buf[dst+3] = a
			dst += 4
		}
	}

	// Convert RGBA to BGR using OpenCV's optimized SIMD conversion
	mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC4, buf)
	if err != nil {
		return gocv.NewMat(), fmt.Errorf("imgconv: failed to create Mat from RGBA: %v", err)
	}

	result := gocv.NewMat()
	gocv.CvtColor(mat, &result, gocv.ColorRGBAToBGR)
	mat.Close()
	return result, nil
}

// convertNRGBA handles non-premultiplied RGBA (no unpremultiply needed)
func convertNRGBA(im *image.NRGBA) (gocv.Mat, error) {
	w, h := im.Rect.Dx(), im.Rect.Dy()

	// Check if we can avoid repacking
	if im.Stride == 4*w && im.Rect.Min.Eq(image.Point{}) {
		// Direct conversion - NewMatFromBytes copies the data
		mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC4, im.Pix)
		if err != nil {
			return gocv.NewMat(), fmt.Errorf("imgconv: failed to create Mat from NRGBA: %v", err)
		}

		dst := gocv.NewMat()
		gocv.CvtColor(mat, &dst, gocv.ColorRGBAToBGR)
		mat.Close()
		return dst, nil
	}

	// Need to repack due to stride or non-zero origin
	buf := make([]byte, 4*w*h)
	dst := 0
	y0, x0 := im.Rect.Min.Y, im.Rect.Min.X

	for y := 0; y < h; y++ {
		src := (y+y0)*im.Stride + x0*4
		copy(buf[dst:dst+4*w], im.Pix[src:src+4*w])
		dst += 4 * w
	}

	mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC4, buf)
	if err != nil {
		return gocv.NewMat(), fmt.Errorf("imgconv: failed to create Mat from repacked NRGBA: %v", err)
	}

	result := gocv.NewMat()
	gocv.CvtColor(mat, &result, gocv.ColorRGBAToBGR)
	mat.Close()
	return result, nil
}

// convertGray handles grayscale images efficiently
func convertGray(im *image.Gray) (gocv.Mat, error) {
	w, h := im.Rect.Dx(), im.Rect.Dy()

	if im.Stride == w && im.Rect.Min.Eq(image.Point{}) {
		// Direct use if tightly packed
		mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC1, im.Pix)
		if err != nil {
			return gocv.NewMat(), fmt.Errorf("imgconv: failed to create Mat from Gray: %v", err)
		}

		dst := gocv.NewMat()
		gocv.CvtColor(mat, &dst, gocv.ColorGrayToBGR)
		mat.Close()
		return dst, nil
	}

	// Repack if stride doesn't match or non-zero origin
	buf := make([]byte, w*h)
	dst := 0
	y0, x0 := im.Rect.Min.Y, im.Rect.Min.X

	for y := 0; y < h; y++ {
		src := (y+y0)*im.Stride + x0
		copy(buf[dst:dst+w], im.Pix[src:src+w])
		dst += w
	}

	mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC1, buf)
	if err != nil {
		return gocv.NewMat(), fmt.Errorf("imgconv: failed to create Mat from repacked Gray: %v", err)
	}

	result := gocv.NewMat()
	gocv.CvtColor(mat, &result, gocv.ColorGrayToBGR)
	mat.Close()
	return result, nil
}

// convertYCbCrManual uses lookup tables for all YCbCr formats
// This handles all subsampling ratios correctly and works across all GoCV versions
func convertYCbCrManual(im *image.YCbCr) (gocv.Mat, error) {
	initYCbCrTables()

	bounds := im.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	mat := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)

	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close()
		return gocv.NewMat(), fmt.Errorf("imgconv: failed to get Mat data pointer: %v", err)
	}

	// Process each pixel, letting YOffset/COffset handle subsampling correctly
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// YOffset/COffset handle any subsampling ratio and odd origins correctly
			yi := im.YOffset(x+bounds.Min.X, y+bounds.Min.Y)
			ci := im.COffset(x+bounds.Min.X, y+bounds.Min.Y)

			yy := int32(im.Y[yi])
			cb := im.Cb[ci]
			cr := im.Cr[ci]

			// Fast RGB calculation using lookup tables (no multiplications)
			r := clamp(yy + ycbcrTable.cr2r[cr])
			g := clamp(yy - ycbcrTable.cb2g[cb] - ycbcrTable.cr2g[cr])
			b := clamp(yy + ycbcrTable.cb2b[cb])

			// Write as BGR for OpenCV
			idx := (y*w + x) * 3
			matData[idx+0] = b
			matData[idx+1] = g
			matData[idx+2] = r
		}
	}

	return mat, nil
}

// convertGeneric handles any image type via the generic At() interface
func convertGeneric(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	mat := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)

	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close()
		return gocv.NewMat(), fmt.Errorf("imgconv: failed to get Mat data pointer: %v", err)
	}

	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// Convert from 16-bit to 8-bit and write as BGR
			matData[idx+0] = uint8(b >> 8)
			matData[idx+1] = uint8(g >> 8)
			matData[idx+2] = uint8(r >> 8)
			idx += 3
		}
	}

	return mat, nil
}

// initYCbCrTables initializes lookup tables for YCbCr->RGB conversion
func initYCbCrTables() {
	ycbcrOnce.Do(func() {
		// BT.601 full range (no 16/235 offsets)
		// Using fixed-point arithmetic with rounding for better accuracy
		for i := 0; i < 256; i++ {
			cb := int32(i) - 128
			cr := int32(i) - 128

			// Add (1<<15) for rounding instead of truncation
			ycbcrTable.cr2r[i] = (91881*cr + (1 << 15)) >> 16
			ycbcrTable.cb2b[i] = (116130*cb + (1 << 15)) >> 16
			ycbcrTable.cr2g[i] = (46802*cr + (1 << 15)) >> 16
			ycbcrTable.cb2g[i] = (22554*cb + (1 << 15)) >> 16
		}
	})
}

// clamp ensures value is in valid byte range [0, 255]
func clamp(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
